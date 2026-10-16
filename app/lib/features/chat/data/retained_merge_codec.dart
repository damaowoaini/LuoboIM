import 'dart:convert';

import 'package:crypto/crypto.dart';

/// Bounded, lossless retained protocol payload apart from local-only paths.
/// A new107 root owns the destination key; child keyVersion is preserved.
Map<String, dynamic> retainedMergeChild(Map<String, dynamic> wire) {
  var nodes = 0;
  Object? visit(Object? value, int depth) {
    if (depth > 64 || ++nodes > 32768) throw const FormatException();
    if (value is List) return value.map((v) => visit(v, depth + 1)).toList();
    if (value is Map) {
      return <String, dynamic>{
        for (final entry in value.entries)
          if (entry.key is String)
            entry.key as String:
                const {
                  'sourcePath',
                  'soundPath',
                  'videoPath',
                  'snapshotPath',
                  'filePath',
                  'localEx',
                }.contains(entry.key)
                ? null
                : visit(entry.value, depth + 1),
      };
    }
    return value;
  }

  final result = visit(wire, 0) as Map<String, dynamic>;
  if (jsonEncode(result).length > 2 * 1024 * 1024) {
    throw const FormatException();
  }
  return result;
}

String retainedMergeFingerprint(Map<String, dynamic> wire) {
  Object? canonical(Object? value) {
    if (value is List) return value.map(canonical).toList();
    if (value is Map) {
      final keys = value.keys.cast<String>().toList()..sort();
      return {for (final key in keys) key: canonical(value[key])};
    }
    return value;
  }

  return sha256
      .convert(utf8.encode(jsonEncode(canonical(retainedMergeChild(wire)))))
      .toString();
}
