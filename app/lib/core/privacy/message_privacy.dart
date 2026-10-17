import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';

/// Explicit OpenIM burn metadata, independent from encryption or chat type.
final class MessagePrivacy {
  const MessagePrivacy({
    this.isPrivateChat,
    this.burnDurationSeconds,
    this.messageReadAtMilliseconds,
    this.attachmentReadAtMilliseconds,
    bool containsPrivateContent = false,
    bool inspectionComplete = true,
    int? expiresAtMilliseconds,
  }) : _containsPrivate = containsPrivateContent,
       _complete = inspectionComplete,
       _expiry = expiresAtMilliseconds,
       _memory = null,
       _clock = null,
       _hide = null,
       changes = null;

  MessagePrivacy._remembered(
    MessagePrivacy source,
    this._memory,
    this._clock,
    this._hide, {
    this.changes,
  }) : isPrivateChat = source.isPrivateChat,
       burnDurationSeconds = source.burnDurationSeconds,
       messageReadAtMilliseconds = source.messageReadAtMilliseconds,
       attachmentReadAtMilliseconds = source.attachmentReadAtMilliseconds,
       _containsPrivate = source.containsPrivateContent,
       _complete = source.inspectionComplete,
       _expiry = source.expiresAtMilliseconds;
  factory MessagePrivacy.combined(List<MessagePrivacy> values) {
    if (values.isEmpty) return const MessagePrivacy();
    return MessagePrivacy._remembered(
      const MessagePrivacy(),
      () {
        final result = _PrivacyFacts();
        for (final value in values) {
          result.private |= value.containsPrivateContent;
          result.complete &= value.inspectionComplete;
          result.expiry = _earliest(result.expiry, value.expiresAtMilliseconds);
          result.hidden |= value.hidden;
        }
        return result;
      },
      (now) {
        var time = now.millisecondsSinceEpoch;
        for (final value in values) {
          time = max(time, value._clock?.call(now) ?? time);
        }
        return time;
      },
      () {
        for (final value in values) {
          value._hide?.call();
        }
      },
      changes: values
          .map((value) => value.changes)
          .whereType<Stream<void>>()
          .firstOrNull,
    );
  }

  final Stream<void>? changes;
  final bool? isPrivateChat;
  final int? burnDurationSeconds;
  final int? messageReadAtMilliseconds;
  final int? attachmentReadAtMilliseconds;
  final bool _containsPrivate, _complete;
  final int? _expiry;
  final _PrivacyFacts Function()? _memory;
  final int Function(DateTime)? _clock;
  final void Function()? _hide;
  bool get containsPrivateContent =>
      _containsPrivate || (_memory?.call().private ?? false);
  bool get inspectionComplete =>
      _complete && (_memory?.call().complete ?? true);
  int? get expiresAtMilliseconds => _earliest(_expiry, _memory?.call().expiry);
  bool get hidden => _memory?.call().hidden ?? false;
  bool isExpiredAt(DateTime now) {
    if (hidden) return true;
    final expiry = expiresAtMilliseconds;
    if (expiry == null ||
        (_clock?.call(now) ?? now.millisecondsSinceEpoch) < expiry) {
      return false;
    }
    _hide?.call();
    return true;
  }

  bool canViewAt(DateTime now) => !isExpiredAt(now);
  bool canRetainAt(DateTime now) => allowsRetention && canViewAt(now);
  int? millisecondsUntilExpiry(DateTime now) {
    if (!canViewAt(now)) return 0;
    final expiry = expiresAtMilliseconds;
    return expiry == null
        ? null
        : max(0, expiry - (_clock?.call(now) ?? now.millisecondsSinceEpoch));
  }

  bool get allowsRetention =>
      inspectionComplete && !containsPrivateContent && !hidden;
  String? get notice => containsPrivateContent
      ? '阅后即焚内容不能收藏或转发，也不能复制、引用或交给外部应用；暂勿依赖本版本自动销毁'
      : !inspectionComplete
      ? '消息隐私状态无法确认，暂不能复制、引用、收藏、转发或交给外部应用'
      : hidden
      ? '消息内容已不可用'
      : null;
}

int? _earliest(int? a, int? b) => a == null
    ? b
    : b == null
    ? a
    : min(a, b);

/// The legacy contract accepts only integral Unix seconds/milliseconds in
/// 2000–2100. Never derive a read time from isRead, sendTime or rendering.
int? normalizePrivateReadTime(Object? readAt) {
  if (readAt is! int) return null;
  if (readAt >= 946684800 && readAt <= 4102444800) return readAt * 1000;
  if (readAt >= 946684800000 && readAt <= 4102444800000) return readAt;
  return null;
}

int? privateExpiryMilliseconds({
  required int? readAt,
  required int? durationSeconds,
}) {
  final milliseconds = normalizePrivateReadTime(readAt);
  if (milliseconds == null || (durationSeconds ?? 0) < 0) return null;
  const maximum = 8640000000000000;
  final duration = durationSeconds == null || durationSeconds == 0
      ? 30
      : durationSeconds;
  if (duration > (maximum - milliseconds) ~/ 1000) return null;
  return milliseconds + duration * 1000;
}

/// Actual protocol descendants only. Opaque custom JSON is never interpreted.
Iterable<Object?> actualMessageChildren(Map wire) sync* {
  for (final field in ['quoteElem', 'atTextElem']) {
    final element = wire[field];
    if (element is Map && element['quoteMessage'] != null) {
      yield element['quoteMessage'];
    }
  }
  final merge = wire['mergeElem'];
  if (merge is Map && merge['multiMessage'] is List) {
    yield* merge['multiMessage'] as List;
  }
}

MessagePrivacy inspectMessagePrivacy(Object? wire) {
  var complete = true, containsPrivate = false;
  var nodes = 0;
  int? expiry;
  Map? attachment;
  bool? rootPrivate;
  final ancestors = Set<Object>.identity();
  void inspect(Object? value, int depth, {bool root = false}) {
    if (value == null && !root) return;
    if (depth > 32 ||
        ++nodes > 4096 ||
        value is! Map ||
        !ancestors.add(value)) {
      complete = false;
      return;
    }
    final sources = <Map>[];
    final encoded = value['attachedInfo'];
    if (encoded != null && encoded != '') {
      if (encoded is! String || encoded.length > 65536) {
        complete = false;
      } else {
        try {
          final decoded = jsonDecode(encoded);
          if (decoded is Map) {
            sources.add(decoded);
          } else if (decoded != null) {
            complete = false;
          }
        } catch (_) {
          complete = false;
        }
      }
    }
    final modeled = value['attachedInfoElem'];
    if (modeled is Map) {
      sources.add(modeled);
    } else if (modeled != null) {
      complete = false;
    }
    if (value['hasReadTime'] != null && value['hasReadTime'] is! int) {
      complete = false;
    }
    final reads = [
      value['hasReadTime'],
      for (final source in sources) source['hasReadTime'],
    ];
    for (final info in sources) {
      final flag = info['isPrivateChat'];
      if (flag != null && flag is! bool) complete = false;
      for (final field in ['burnDuration', 'hasReadTime']) {
        if (info[field] != null && info[field] is! int) complete = false;
      }
      if (info['burnDuration'] is int && (info['burnDuration'] as int) < 0) {
        complete = false;
      }
      if (flag != true) continue;
      containsPrivate = true;
      if (root) rootPrivate = true;
      final duration = info['burnDuration'];
      if (duration == null || duration is int) {
        for (final read in reads) {
          if (read is int) {
            expiry = _earliest(
              expiry,
              privateExpiryMilliseconds(
                readAt: read,
                durationSeconds: duration as int?,
              ),
            );
          }
        }
      }
    }
    if (root) {
      attachment = sources.isEmpty ? null : sources.last;
      rootPrivate ??= attachment?['isPrivateChat'] is bool
          ? attachment!['isPrivateChat'] as bool
          : null;
    }
    for (final field in ['quoteElem', 'atTextElem']) {
      final quote = value[field];
      if (quote is Map) {
        inspect(quote['quoteMessage'], depth + 1);
      } else if (quote != null) {
        complete = false;
      }
    }
    final merge = value['mergeElem'];
    if (merge is Map) {
      final children = merge['multiMessage'];
      if (children is List) {
        for (final child in children) {
          if (nodes >= 4096) {
            complete = false;
            break;
          }
          inspect(child, depth + 1);
        }
      } else {
        complete = false;
      }
    } else if (merge != null) {
      complete = false;
    }
    ancestors.remove(value);
  }

  inspect(wire, 0, root: true);
  return MessagePrivacy(
    isPrivateChat: rootPrivate,
    burnDurationSeconds: attachment?['burnDuration'] is int
        ? attachment!['burnDuration'] as int
        : null,
    messageReadAtMilliseconds: wire is Map && wire['hasReadTime'] is int
        ? wire['hasReadTime'] as int
        : null,
    attachmentReadAtMilliseconds: attachment?['hasReadTime'] is int
        ? attachment!['hasReadTime'] as int
        : null,
    containsPrivateContent: containsPrivate,
    inspectionComplete: complete,
    expiresAtMilliseconds: expiry,
  );
}

final class _PrivacyFacts {
  _PrivacyFacts({
    this.private = false,
    this.complete = true,
    this.expiry,
    this.hidden = false,
  });
  bool private, complete, hidden;
  int? expiry;
  final Set<String> children = {}, conversations = {};
  String get fingerprint =>
      '$private/$complete/$expiry/$hidden/${children.length}';
}

/// Memory for one authenticated owner/session. Contains IDs and privacy facts
/// only: never message text, filenames, URLs, credentials or SDK model objects.
/// It survives Router/repository/ordinary-lock remounts, not process restarts.
final class MessagePrivacyLedger {
  MessagePrivacyLedger({
    int Function()? monotonicMilliseconds,
    this.maximumEntries = 50000,
  }) {
    final stopwatch = Stopwatch()..start();
    _monotonic = monotonicMilliseconds ?? (() => stopwatch.elapsedMilliseconds);
  }
  final int maximumEntries;
  late final int Function() _monotonic;
  final _changes = StreamController<void>.broadcast();
  final Map<String, _PrivacyFacts> _entries = {};
  final Set<String> _retentionDenied = {};
  bool _retentionCapacityExceeded = false;
  int _scope = 0;
  int? _floor, _lastMono;
  String? _owner;
  int? _generation;
  bool _disposed = false;
  Stream<void> get changes => _changes.stream;
  bool isBoundTo(String owner, int generation) =>
      !_disposed && _owner == owner && _generation == generation;
  void bind(String owner, int generation) {
    if (_owner == owner && _generation == generation) return;
    ++_scope;
    _entries.clear();
    _retentionDenied.clear();
    _retentionCapacityExceeded = false;
    _floor = _lastMono = null;
    _owner = owner;
    _generation = generation;
    _publish();
  }

  void endSession() {
    ++_scope;
    _owner = null;
    _generation = null;
    _entries.clear();
    _retentionDenied.clear();
    _retentionCapacityExceeded = false;
    _floor = _lastMono = null;
    _publish();
  }

  void _publish() {
    if (!_disposed) _changes.add(null);
  }

  int _now(DateTime wall) {
    final mono = _monotonic();
    final int time = max<int>(
      wall.millisecondsSinceEpoch,
      (_floor ?? wall.millisecondsSinceEpoch) +
          max(0, mono - (_lastMono ?? mono)),
    );
    _floor = time;
    _lastMono = mono;
    return time;
  }

  _PrivacyFacts _resolve(String id, int scope) {
    final memo = <String, _PrivacyFacts>{};
    final ancestors = <String>{};
    var nodes = 0, edges = 0;
    _PrivacyFacts resolve(String key) {
      if (scope != _scope || _owner == null || _disposed) {
        return _PrivacyFacts(complete: false, hidden: true);
      }
      final cached = memo[key];
      if (cached != null) return cached;
      final value = _entries[key];
      if (value == null) return _PrivacyFacts(complete: false, hidden: true);
      if (++nodes > 4096 || ancestors.length > 32 || !ancestors.add(key)) {
        return _PrivacyFacts(
          private: value.private,
          complete: false,
          expiry: value.expiry,
          hidden: true,
        );
      }
      final result = _PrivacyFacts(
        private: value.private,
        complete: value.complete,
        expiry: value.expiry,
        hidden: value.hidden,
      );
      for (final child in value.children) {
        if (++edges > 8192) {
          result.complete = false;
          result.hidden = true;
          break;
        }
        final nested = resolve(child);
        result.private |= nested.private;
        result.complete &= nested.complete;
        result.expiry = _earliest(result.expiry, nested.expiry);
        result.hidden |= nested.hidden;
      }
      ancestors.remove(key);
      memo[key] = result;
      return result;
    }

    return resolve(id);
  }

  MessagePrivacy _project(String id, MessagePrivacy base, int scope) =>
      MessagePrivacy._remembered(base, () => _resolve(id, scope), _now, () {
        if (scope == _scope) forget([id]);
      }, changes: _changes.stream);
  MessagePrivacy observe(
    String conversationId,
    Object? wire, {
    required DateTime now,
  }) {
    final scope = _scope;
    if (_owner == null || _disposed) {
      return MessagePrivacy._remembered(
        inspectMessagePrivacy(wire),
        () => _PrivacyFacts(complete: false, hidden: true),
        _now,
        () {},
        changes: _changes.stream,
      );
    }
    final clock = _now(now);
    var nodes = 0, changed = false;
    final path = Set<Object>.identity();
    String? remember(Object? object, int depth) {
      if (object is! Map || depth > 32 || ++nodes > 4096 || !path.add(object)) {
        return null;
      }
      final id = object['clientMsgID'];
      final privacy = inspectMessagePrivacy(object);
      final children = <String>{};
      for (final child in actualMessageChildren(object)) {
        final key = remember(child, depth + 1);
        if (key != null) children.add(key);
      }
      path.remove(object);
      if (id is! String || id.isEmpty || id.length > 512) return null;
      if (!_entries.containsKey(id) && _entries.length >= maximumEntries) {
        return null;
      }
      final entry = _entries.putIfAbsent(id, () => _PrivacyFacts());
      final before = entry.fingerprint;
      entry.private |= privacy.containsPrivateContent;
      entry.complete &= privacy.inspectionComplete;
      entry.expiry = _earliest(entry.expiry, privacy.expiresAtMilliseconds);
      entry.hidden |= entry.expiry != null && clock >= entry.expiry!;
      entry.children.addAll(children);
      entry.conversations.add(conversationId);
      changed |= before != entry.fingerprint;
      return id;
    }

    final key = remember(wire, 0);
    if (changed) _publish();
    final fresh = inspectMessagePrivacy(wire);
    if (key == null) {
      // Missing/exhausted identity cannot be remembered safely across remounts.
      return MessagePrivacy._remembered(
        fresh,
        () => _PrivacyFacts(
          complete: false,
          hidden: _entries.length >= maximumEntries,
        ),
        _now,
        () {},
        changes: _changes.stream,
      );
    }
    return _project(key, fresh, scope);
  }

  /// An explicitly retained ordinary favorite remains independent when its
  /// ordinary source is removed. Private/incomplete source facts still win,
  /// including facts learned after this reference was created.
  MessagePrivacy retentionReference(String id) {
    final scope = _scope;
    return MessagePrivacy._remembered(
      const MessagePrivacy(),
      () {
        if (scope != _scope || _owner == null || _disposed) {
          return _PrivacyFacts(complete: false, hidden: true);
        }
        if (!_entries.containsKey(id)) {
          return _PrivacyFacts(complete: _entries.length < maximumEntries);
        }
        final value = _resolve(id, scope);
        return _PrivacyFacts(
          private: value.private,
          complete: value.complete,
          expiry: value.private ? value.expiry : null,
          hidden: value.private && value.hidden,
        );
      },
      _now,
      () {},
      changes: _changes.stream,
    );
  }

  // Server-side retention denial is independent of chat viewing/private flags.
  // Hash arbitrary favorite source identifiers so this metadata store cannot
  // retain an old URL supplied as a legacy sourceID.
  Iterable<String> _retentionKeys(String favoriteId, String sourceId) sync* {
    for (final entry in [('favorite', favoriteId), ('source', sourceId)]) {
      if (entry.$2.isNotEmpty) {
        yield '${entry.$1}:${sha256.convert(utf8.encode(entry.$2))}';
      }
    }
  }

  void denyFavoriteRetention(String favoriteId, String sourceId) {
    if (_owner == null || _disposed) return;
    var changed = false;
    for (final key in _retentionKeys(favoriteId, sourceId)) {
      if (_retentionDenied.contains(key)) continue;
      if (_retentionDenied.length >= maximumEntries) {
        changed |= !_retentionCapacityExceeded;
        _retentionCapacityExceeded = true;
        break;
      }
      changed |= _retentionDenied.add(key);
    }
    if (changed) _publish();
  }

  MessagePrivacy favoriteRetentionReference(
    String favoriteId,
    String sourceId,
  ) {
    final scope = _scope;
    final keys = _retentionKeys(favoriteId, sourceId).toList(growable: false);
    return MessagePrivacy._remembered(
      const MessagePrivacy(),
      () => _PrivacyFacts(
        complete:
            scope == _scope &&
            _owner != null &&
            !_disposed &&
            !_retentionCapacityExceeded &&
            !keys.any(_retentionDenied.contains),
      ),
      _now,
      () {},
      changes: _changes.stream,
    );
  }

  MessagePrivacy lookup(String id) =>
      _project(id, const MessagePrivacy(), _scope);
  void forget(Iterable<String> ids) {
    var changed = false;
    for (final id in ids) {
      if (id.isEmpty || id.length > 512) continue;
      final entry = _entries[id];
      if (entry != null) {
        if (!entry.hidden) {
          entry.hidden = true;
          changed = true;
        }
      } else if (_entries.length < maximumEntries) {
        _entries[id] = _PrivacyFacts(hidden: true);
        changed = true;
      }
    }
    if (changed) _publish();
  }

  void hideKnown({String? conversationId}) => forget(
    _entries.entries
        .where(
          (entry) =>
              conversationId == null ||
              entry.value.conversations.contains(conversationId),
        )
        .map((entry) => entry.key)
        .toList(),
  );
  Future<void> dispose() async {
    endSession();
    _disposed = true;
    await _changes.close();
  }
}
