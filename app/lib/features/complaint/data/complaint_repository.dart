import 'dart:convert';
import 'dart:math';

import '../../../core/error/app_failure.dart';
import '../../../core/network/tenant_api.dart';

enum ComplaintTarget { user, group }

final class ComplaintRecord {
  ComplaintRecord({
    required this.id,
    required this.targetId,
    required this.targetName,
    required this.reason,
    required this.description,
    required this.status,
    required this.evidence,
    required this.createdAt,
    required this.result,
  });
  final String id, targetId, targetName, reason, description, result;
  final int status;
  final List<Uri> evidence;
  final DateTime? createdAt;
}

final class ComplaintRepository {
  ComplaintRepository(this.api);
  final TenantApi api;
  bool _replaySafe = false;
  bool get isCurrent => api.access.isCurrent;
  bool get canReplay => isCurrent && _replaySafe;
  void check() => api.access.check();

  static const userReasons = ['骚扰或辱骂', '垃圾广告', '不当内容', '冒充他人', '欺诈', '其他'];
  static const groupReasons = ['群内不当内容', '群管理失当', '群内欺诈', '群名称不当', '其他'];
  static final _random = Random.secure();
  static final _submission = RegExp(
    r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$',
  );

  static String newSubmissionId() {
    final bytes = List.generate(16, (_) => _random.nextInt(256));
    bytes[6] = (bytes[6] & 15) | 64;
    bytes[8] = (bytes[8] & 63) | 128;
    final hex = bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
    return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
  }

  String _nameSnapshot(String value) {
    final out = StringBuffer();
    var bytes = 0;
    for (final rune in value.runes) {
      final safe = rune < 32 || (rune >= 127 && rune <= 159)
          ? ' '
          : String.fromCharCode(rune);
      final count = utf8.encode(safe).length;
      if (bytes + count > 255) break;
      bytes += count;
      out.write(safe);
    }
    return out.toString();
  }

  bool _text(
    String value,
    int bytes, {
    bool required = false,
    bool multiline = false,
  }) =>
      (!required || value.trim().isNotEmpty) &&
      utf8.encode(value).length <= bytes &&
      !value.runes.any(
        (r) =>
            (r < 32 && !(multiline && [9, 10, 13].contains(r))) ||
            (r >= 127 && r <= 159),
      );

  static Uri? safeImage(Object? value) {
    final uri = value is String ? Uri.tryParse(value) : null;
    return uri != null &&
            uri.scheme == 'https' &&
            uri.host.isNotEmpty &&
            uri.userInfo.isEmpty &&
            !uri.hasFragment &&
            value is String &&
            value.length <= 2048
        ? uri
        : null;
  }

  Future<String> submit({
    required String submissionId,
    required ComplaintTarget target,
    required String targetId,
    required String targetName,
    required String reason,
    String description = '',
    List<Uri> evidence = const [],
  }) async {
    check();
    if (!_submission.hasMatch(submissionId) ||
        !_text(targetId, 64, required: true) ||
        targetId.trim() != targetId ||
        !_text(reason, 500, required: true) ||
        !_text(description, 4096, multiline: true) ||
        evidence.length > 9 ||
        evidence.toSet().length != evidence.length ||
        evidence.any((uri) => safeImage(uri.toString()) == null)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    try {
      final data = await api.chat.post('complaint/submit', {
        'clientSubmissionID': submissionId,
        'complaintUserID': api.access.credentials.userId,
        'complaintType': target == ComplaintTarget.user ? 1 : 2,
        'targetID': targetId,
        'targetName': _nameSnapshot(targetName),
        'reason': reason,
        'description': description,
        'evidence': evidence.map((u) => u.toString()).toList(),
      });
      check();
      final id = data is Map ? data['complaintID'] : null;
      if (id is! String || !_text(id, 64, required: true)) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      return id;
    } catch (error) {
      final definite =
          error is AppFailure &&
          (error.code == 1001 ||
              [
                FailureKind.permissionDenied,
                FailureKind.invalidInput,
                FailureKind.sessionExpired,
                FailureKind.rateLimited,
              ].contains(error.kind));
      if (isCurrent && !definite) {
        try {
          // Missing does not prove the original is not still committing.
          // Only query here; never submit another complaint with a new ID.
          final recovered = await receipt(submissionId)
              .timeout(const Duration(seconds: 3));
          if (recovered != null) return recovered;
        } catch (_) {}
      }
      rethrow;
    }
  }

  Future<String?> receipt(String submissionId) async {
    check();
    _replaySafe = false;
    if (!_submission.hasMatch(submissionId)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final raw = await api.chat.post('complaint/receipt', {
      'clientSubmissionID': submissionId,
    });
    check();
    if (raw is! Map || raw['found'] is! bool) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    if (raw['found'] == false) {
      _replaySafe = raw['replaySafe'] == true;
      return null;
    }
    final id = raw['complaintID'];
    if (raw['clientSubmissionID'] != submissionId ||
        id is! String ||
        !_text(id, 64, required: true) ||
        ![0, 1, 2].contains(raw['status'])) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    _replaySafe = raw['replaySafe'] == true;
    return id;
  }

  ComplaintRecord _record(Object? value) {
    if (value is! Map ||
        value['complaintUserID'] != api.access.credentials.userId ||
        value['id'] is! String ||
        (value['id'] as String).isEmpty ||
        value['status'] is! int ||
        ![0, 1, 2].contains(value['status'])) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final raw = value['evidence'];
    Object? images = raw;
    if (raw is String) {
      try {
        images = jsonDecode(raw);
      } catch (_) {
        images = null;
      }
    }
    if (images != null && images is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    // Legacy unsafe/malformed image URLs are not loaded into a browser/image
    // decoder. Text and the complaint remain available for recovery.
    final evidence = (images as List? ?? [])
        .take(9)
        .map(safeImage)
        .whereType<Uri>()
        .toList();
    String text(String key) => value[key] is String ? value[key] as String : '';
    return ComplaintRecord(
      id: text('id'),
      targetId: text('targetID'),
      targetName: text('targetName'),
      reason: text('reason'),
      description: text('description'),
      status: value['status'] as int,
      evidence: List.unmodifiable(evidence),
      createdAt: DateTime.tryParse(text('createTime')),
      result: text('handleResult'),
    );
  }

  Future<({int total, List<ComplaintRecord> records})> search({
    int page = 1,
  }) async {
    check();
    if (page < 1 || page > 100000) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final raw = await api.chat.post('complaint/search', {
      'complaintUserID': api.access.credentials.userId,
      'pageNumber': page,
      'showNumber': 20,
    });
    check();
    if (raw is! Map ||
        raw['total'] is! int ||
        (raw['total'] as int) < 0 ||
        (raw['complaints'] != null && raw['complaints'] is! List)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return (
      total: raw['total'] as int,
      records: (raw['complaints'] as List? ?? []).map(_record).toList(),
    );
  }
}
