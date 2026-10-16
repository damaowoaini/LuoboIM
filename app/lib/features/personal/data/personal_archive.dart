import 'dart:convert';

import 'package:crypto/crypto.dart';

import '../../../core/config/app_config.dart';
import '../../../core/error/app_failure.dart';
import '../domain/personal_item.dart';

final class PersonalArchive {
  const PersonalArchive._({
    required this.items,
    required this.fingerprint,
    this.owner,
  });
  static const maxBytes = 16 * 1024 * 1024;
  static const maxItems = 10000;
  final List<PersonalItem> items;
  final String fingerprint;
  final String? owner;

  factory PersonalArchive.decode(List<int> bytes) {
    const invalid = AppFailure(FailureKind.invalidInput);
    if (bytes.length > maxBytes) throw invalid;
    try {
      final raw = jsonDecode(utf8.decode(bytes));
      if (raw is! Map) throw invalid;
      final legacy = raw['format'] == null;
      if (!legacy &&
          (raw['format'] != 'luobo-personal-data' || raw['version'] != 1)) {
        throw invalid;
      }
      final owner = raw['owner'];
      if (!legacy &&
          (owner is! String || !RegExp(r'^[a-f0-9]{64}$').hasMatch(owner))) {
        throw invalid;
      }
      final items = <PersonalItem>[];
      for (final kind in PersonalKind.values) {
        final key = kind == PersonalKind.note ? 'notes' : 'todos';
        var values = raw[key] ?? raw['local_$key'];
        if (values is String) values = jsonDecode(values);
        if (values == null && legacy) continue;
        if (values is! List || values.length + items.length > maxItems) {
          throw invalid;
        }
        for (final value in values) {
          items.add(PersonalItem.fromJson(kind, value));
        }
      }
      if (legacy &&
          ![
            'notes',
            'todos',
            'local_notes',
            'local_todos',
          ].any(raw.containsKey)) {
        throw invalid;
      }
      final keys = <String>{};
      for (final item in items) {
        if (!keys.add('${item.kind.name}:${item.id}')) throw invalid;
      }
      return PersonalArchive._(
        items: List.unmodifiable(items),
        owner: legacy ? null : owner as String,
        fingerprint: sha256.convert(bytes).toString(),
      );
    } on AppFailure {
      rethrow;
    } catch (_) {
      throw invalid;
    }
  }

  static List<int> encode(String owner, List<PersonalItem> items) {
    final parts = encodeParts(owner, items);
    if (parts.length != 1) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return parts.single;
  }

  /// Every part remains an independently restorable version1 archive.
  /// Count the actual UTF8 JSON bytes, including escaping and the envelope.
  static List<List<int>> encodeParts(String owner, List<PersonalItem> items) {
    const invalid = AppFailure(FailureKind.invalidInput);
    if (!RegExp(r'^[a-f0-9]{64}$').hasMatch(owner)) throw invalid;
    final exportedAt = DateTime.now().toUtc().toIso8601String();
    final envelopeBytes = _encode(owner, const [], exportedAt).length;
    final parts = <List<int>>[];
    var batch = <PersonalItem>[];
    var bytes = envelopeBytes;
    final ids = <String>{};
    void flush() {
      final part = _encode(owner, batch, exportedAt);
      if (part.length > maxBytes || batch.length > maxItems) throw invalid;
      parts.add(part);
      batch = [];
      bytes = envelopeBytes;
    }

    for (final item in items) {
      final value = item.toJson();
      PersonalItem.fromJson(item.kind, value);
      if (!ids.add('${item.kind.name}:${item.id}')) throw invalid;
      // One extra comma per item is conservative for the two separate arrays.
      final itemBytes = utf8.encode(jsonEncode(value)).length + 1;
      if (envelopeBytes + itemBytes > maxBytes) throw invalid;
      if (batch.isNotEmpty &&
          (batch.length == maxItems || bytes + itemBytes > maxBytes)) {
        flush();
      }
      batch.add(item);
      bytes += itemBytes;
    }
    if (batch.isNotEmpty || parts.isEmpty) flush();
    return List.unmodifiable(parts);
  }

  static List<int> _encode(
    String owner,
    List<PersonalItem> items,
    String exportedAt,
  ) => utf8.encode(
    jsonEncode({
      'format': 'luobo-personal-data',
      'version': 1,
      'applicationId': Brand.applicationId,
      'owner': owner,
      'exportedAt': exportedAt,
      'notes': items
          .where((item) => item.kind == PersonalKind.note)
          .map((item) => item.toJson())
          .toList(),
      'todos': items
          .where((item) => item.kind == PersonalKind.todo)
          .map((item) => item.toJson())
          .toList(),
    }),
  );
}
