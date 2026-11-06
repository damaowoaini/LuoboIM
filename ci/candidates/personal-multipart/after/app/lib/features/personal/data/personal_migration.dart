// Also copied, unchanged, into the signed OLD app's lib/migration/.
// No Flutter, preferences or account lookup: ownership is deliberately unknown.
import 'dart:convert';

import 'package:crypto/crypto.dart';

const personalMigrationMaxBytes = 16 * 1024 * 1024;
const personalMigrationMaxItems = 10000;

String personalMigrationSha(List<int> bytes) =>
    sha256.convert(bytes).toString();

Never _bad() => throw const FormatException('Invalid migration data');
Map<String, dynamic> _map(Object? value) {
  if (value is! Map<String, dynamic>) _bad();
  return value;
}

dynamic _freeze(dynamic value) {
  if (value is Map<String, dynamic>) {
    return Map<String, dynamic>.unmodifiable(
      value.map((key, child) => MapEntry(key, _freeze(child))),
    );
  }
  if (value is List) return List<dynamic>.unmodifiable(value.map(_freeze));
  return value;
}

int _count(Object? value) {
  if (value is! int || value < 0 || value > 9007199254740991) _bad();
  return value;
}

String _hash(Object? value) {
  if (value is! String || !RegExp(r'^[a-f0-9]{64}$').hasMatch(value)) _bad();
  return value;
}

void _keys(Map<String, dynamic> value, Set<String> keys) {
  if (value.length != keys.length || !keys.containsAll(value.keys)) _bad();
}

/// Conservative adapter for the CURRENT model, not a claim about unknown old
/// fields. Unsupported values stay in original.json and are reported by offset.
Map<String, dynamic> personalMigrationItem(String kind, Object? value) {
  final item = _map(value);
  if (kind != 'note' && kind != 'todo') _bad();
  final allowed = {
    'id',
    'title',
    'category',
    'createdAt',
    'updatedAt',
    if (kind == 'note') ...['content', 'isPinned'],
    if (kind == 'todo') ...[
      'description',
      'isCompleted',
      'priority',
      'dueDate',
    ],
  };
  if (!allowed.containsAll(item.keys)) _bad();
  void text(String key, int maximum, {bool optional = false}) {
    final value = item[key];
    if (value == null && optional) return;
    if (value is! String || value.length > maximum) _bad();
  }

  void date(String key, {bool optional = false}) {
    final value = item[key];
    if (value == null && optional) return;
    final parsed = value is String ? DateTime.tryParse(value) : null;
    if (parsed == null || parsed.year < 1970 || parsed.year > 9999) _bad();
  }

  text('id', 256);
  if (item['id'] == '') _bad();
  text('title', 1000);
  text(
    kind == 'note' ? 'content' : 'description',
    1000000,
    optional: kind == 'todo',
  );
  text('category', 200, optional: true);
  date('createdAt');
  date('updatedAt', optional: true);
  if (kind == 'todo') date('dueDate', optional: true);
  final boolean = item[kind == 'note' ? 'isPinned' : 'isCompleted'];
  if (boolean != null && boolean is! bool) _bad();
  final priority = item['priority'];
  if (priority != null && (priority is! int || priority < 0 || priority > 2)) {
    _bad();
  }
  return item;
}

class PersonalMigrationManifest {
  PersonalMigrationManifest._(this.data, this.fingerprint);
  final Map<String, dynamic> data;
  final String fingerprint;
  String get setId => data['set'] as String;
  List<Map<String, dynamic>> get parts =>
      (data['parts'] as List).cast<Map<String, dynamic>>();
  int get rejected => data['rejected'] as int;
  bool get incomplete => (data['structuralErrors'] as List).isNotEmpty;

  factory PersonalMigrationManifest.decode(List<int> bytes) {
    if (bytes.length > personalMigrationMaxBytes) _bad();
    final value = _map(jsonDecode(utf8.decode(bytes)));
    _keys(value, {
      'format',
      'version',
      'applicationId',
      'owner',
      'set',
      'raw',
      'report',
      'sourceCounts',
      'rejected',
      'structuralErrors',
      'parts',
    });
    if (value['format'] != 'luobo-personal-migration' ||
        value['version'] is! int ||
        value['version'] != 1 ||
        value['applicationId'] != 'com.luoboduihua.imapp' ||
        value['owner'] != null) {
      _bad();
    }
    final setId = _hash(value['set']);
    for (final name in ['raw', 'report']) {
      final file = _map(value[name]);
      _keys(file, {'file', 'bytes', 'sha256'});
      if (file['file'] !=
          (name == 'raw' ? 'original.json' : 'unmigrated.json')) {
        _bad();
      }
      _count(file['bytes']);
      _hash(file['sha256']);
    }
    if (_map(value['raw'])['sha256'] != setId) _bad();
    final counts = _map(value['sourceCounts']);
    _keys(counts, {'note', 'todo'});
    for (final count in counts.values) {
      if (count != null) _count(count);
    }
    final errors = value['structuralErrors'];
    if (errors is! List ||
        errors.toSet().length != errors.length ||
        errors.any((e) => e != 'note' && e != 'todo')) {
      _bad();
    }
    for (final kind in ['note', 'todo']) {
      if ((counts[kind] == null) != errors.contains(kind)) _bad();
    }
    final rejected = _count(value['rejected']);
    final parts = value['parts'];
    if (parts is! List || parts.length > 10000) _bad();
    final previous = <String, int>{'note': -1, 'todo': -1};
    var accepted = 0;
    for (var index = 0; index < parts.length; index++) {
      final part = _map(parts[index]);
      _keys(part, {'index', 'file', 'bytes', 'sha256', 'sources'});
      if (_count(part['index']) != index ||
          part['file'] != 'part-$index.json' ||
          _count(part['bytes']) > personalMigrationMaxBytes) {
        _bad();
      }
      _hash(part['sha256']);
      final sources = _map(part['sources']);
      _keys(sources, {'note', 'todo'});
      var count = 0;
      for (final kind in ['note', 'todo']) {
        final range = sources[kind];
        if (range == null) continue;
        if (range is! List || range.length != 3) _bad();
        final first = _count(range[0]), last = _count(range[1]);
        final length = _count(range[2]);
        if (length == 0 ||
            length > last - first + 1 ||
            first <= previous[kind]! ||
            counts[kind] == null ||
            last >= (counts[kind] as int)) {
          _bad();
        }
        previous[kind] = last;
        count += length;
      }
      if (count == 0 || count > personalMigrationMaxItems) _bad();
      accepted += count;
    }
    final knownTotal = counts.values.whereType<int>().fold(0, (a, b) => a + b);
    if (knownTotal != accepted + rejected) _bad();
    return PersonalMigrationManifest._(
      _freeze(value) as Map<String, dynamic>,
      personalMigrationSha(bytes),
    );
  }

  List<Map<String, dynamic>> decodePart(int index, List<int> bytes) {
    if (index < 0 || index >= parts.length) _bad();
    final spec = parts[index];
    if (bytes.length != spec['bytes'] ||
        bytes.length > personalMigrationMaxBytes ||
        personalMigrationSha(bytes) != spec['sha256']) {
      _bad();
    }
    final part = _map(jsonDecode(utf8.decode(bytes)));
    _keys(part, {'format', 'version', 'set', 'index', 'records'});
    if (part['format'] != 'luobo-personal-migration-part' ||
        part['version'] is! int ||
        part['version'] != 1 ||
        part['set'] != setId ||
        _count(part['index']) != index) {
      _bad();
    }
    final rawRecords = part['records'];
    if (rawRecords is! List || rawRecords.length > personalMigrationMaxItems) {
      _bad();
    }
    final records = rawRecords.map(_map).toList();
    final actual = <String, List<int>?>{'note': null, 'todo': null};
    for (final record in records) {
      _keys(record, {'kind', 'sourceIndex', 'item'});
      final kind = record['kind'];
      if (kind != 'note' && kind != 'todo') _bad();
      final source = _count(record['sourceIndex']);
      final range = actual[kind];
      if (range == null) {
        actual[kind as String] = [source, source, 1];
      } else {
        if (source <= range[1]) _bad();
        range[1] = source;
        range[2]++;
      }
      personalMigrationItem(kind as String, record['item']);
    }
    for (final kind in ['note', 'todo']) {
      if (jsonEncode(actual[kind]) !=
          jsonEncode((spec['sources'] as Map)[kind])) {
        _bad();
      }
    }
    return records;
  }
}

/// Array spans use UTF16 offsets into the exact preference string, not byte
/// offsets into the enclosing JSON backup. Invalid individual JSON is retained.
List<List<int>> personalMigrationArraySpans(String raw) {
  var position = 0;
  bool space(int c) => c == 32 || c == 9 || c == 10 || c == 13;
  void skip() {
    while (position < raw.length && space(raw.codeUnitAt(position))) {
      position++;
    }
  }

  skip();
  if (position >= raw.length || raw.codeUnitAt(position++) != 91) _bad();
  skip();
  final spans = <List<int>>[];
  if (position < raw.length && raw.codeUnitAt(position) == 93) {
    position++;
    skip();
    if (position != raw.length) _bad();
    return spans;
  }
  while (position < raw.length) {
    final start = position;
    final stack = <int>[];
    var quoted = false, escaped = false;
    for (; position < raw.length; position++) {
      final c = raw.codeUnitAt(position);
      if (quoted) {
        if (escaped) {
          escaped = false;
        } else if (c == 92) {
          escaped = true;
        } else if (c == 34) {
          quoted = false;
        }
      } else if (c == 34) {
        quoted = true;
      } else if (c == 123 || c == 91) {
        stack.add(c);
        if (stack.length > 64) _bad();
      } else if (c == 125 || (c == 93 && stack.isNotEmpty)) {
        if (stack.isEmpty || stack.removeLast() != (c == 125 ? 123 : 91)) {
          _bad();
        }
      } else if (stack.isEmpty && (c == 44 || c == 93)) {
        break;
      }
    }
    if (quoted || stack.isNotEmpty || position == raw.length) _bad();
    var end = position;
    while (end > start && space(raw.codeUnitAt(end - 1))) {
      end--;
    }
    if (end == start) _bad();
    spans.add([start, end]);
    final done = raw.codeUnitAt(position++) == 93;
    skip();
    if (done) {
      if (position != raw.length) _bad();
      return spans;
    }
  }
  _bad();
}

/// The adapter must consume the entire stream, durably close the output, and
/// return its actual size/SHA. The old helper owns all File/Directory access.
class PersonalMigrationFile {
  const PersonalMigrationFile(this.bytes, this.sha256);
  final int bytes;
  final String sha256;
  Map<String, dynamic> descriptor(String name) => {
    'file': name,
    'bytes': bytes,
    'sha256': sha256,
  };
}

typedef PersonalMigrationWriter = Future<PersonalMigrationFile> Function(
  String name,
  Stream<List<int>> chunks,
);

Stream<List<int>> _jsonStringChunks(String value) async* {
  yield const [34];
  for (var start = 0; start < value.length; start += 16384) {
    final end = start + 16384 < value.length ? start + 16384 : value.length;
    // Preserve exact UTF16, including lone surrogates, in bounded ASCII chunks.
    final chunk = StringBuffer();
    for (var i = start; i < end; i++) {
      final c = value.codeUnitAt(i);
      if (c >= 32 && c < 127 && c != 34 && c != 92) {
        chunk.writeCharCode(c);
      } else {
        chunk.write('\\u${c.toRadixString(16).padLeft(4, '0')}');
      }
    }
    yield utf8.encode(chunk.toString());
  }
  yield const [34];
}

Stream<List<int>> _rawChunks(Map<String, String> rawValues) async* {
  yield utf8.encode('{"sourceFormat":"legacy-device-local-v1","local_notes":');
  yield* _jsonStringChunks(rawValues['note']!);
  yield utf8.encode(',"local_todos":');
  yield* _jsonStringChunks(rawValues['todo']!);
  yield const [125];
}

/// Portable source-only export. Raw must finish before any parts/report;
/// manifest is the last output. No sandbox lookup, deletion or account lookup.
Future<PersonalMigrationManifest> exportLegacyPersonalMigration({
  required PersonalMigrationWriter write,
  String? rawNotes,
  String? rawTodos,
}) async {
  final rawValues = {'note': rawNotes ?? '[]', 'todo': rawTodos ?? '[]'};
  final raw = await write('original.json', _rawChunks(rawValues));
  final setId = _hash(raw.sha256);
  final parts = <Map<String, dynamic>>[];
  final sourceCounts = <String, int?>{};
  final structuralErrors = <String>[];
  var rejected = 0, reportRows = 0;
  var records = <Map<String, dynamic>>[];
  var recordBytes = 0;
  List<int> encode() => utf8.encode(
    jsonEncode({
      'format': 'luobo-personal-migration-part',
      'version': 1,
      'set': setId,
      'index': parts.length,
      'records': records,
    }),
  );
  Future<void> flush() async {
    if (records.isEmpty) return;
    final bytes = encode();
    if (bytes.length > personalMigrationMaxBytes) _bad();
    final sources = <String, List<int>?>{'note': null, 'todo': null};
    for (final record in records) {
      final kind = record['kind'] as String;
      final index = record['sourceIndex'] as int;
      final range = sources[kind];
      if (range == null) {
        sources[kind] = [index, index, 1];
      } else {
        range[1] = index;
        range[2]++;
      }
    }
    final name = 'part-${parts.length}.json';
    final output = await write(name, Stream.value(bytes));
    if (output.bytes != bytes.length ||
        output.sha256 != personalMigrationSha(bytes)) {
      _bad();
    }
    parts.add({
      'index': parts.length,
      ...output.descriptor(name),
      'sources': sources,
    });
    records = [];
    recordBytes = 0;
  }

  List<int> refusal(
    String kind,
    int? index,
    int start,
    int end,
    String reason,
  ) {
    final prefix = reportRows++ > 0 ? ',' : '';
    if (index != null) rejected++;
    return utf8.encode(
      prefix +
          jsonEncode({
            'kind': kind,
            'sourceIndex': index,
            'utf16Start': start,
            'utf16End': end,
            'reason': reason,
          }),
    );
  }

  Stream<List<int>> reportChunks() async* {
    yield const [91];
    for (final kind in ['note', 'todo']) {
      final value = rawValues[kind]!;
      late List<List<int>> spans;
      try {
        spans = personalMigrationArraySpans(value);
      } on FormatException {
        sourceCounts[kind] = null;
        structuralErrors.add(kind);
        yield refusal(kind, null, 0, value.length, 'malformed-array');
        continue;
      }
      sourceCounts[kind] = spans.length;
      for (var index = 0; index < spans.length; index++) {
        final span = spans[index];
        if (span[1] - span[0] > personalMigrationMaxBytes) {
          yield refusal(kind, index, span[0], span[1], 'oversized-record');
          continue;
        }
        late Map<String, dynamic> item;
        try {
          item = personalMigrationItem(
            kind,
            jsonDecode(value.substring(span[0], span[1])),
          );
        } on FormatException {
          yield refusal(
            kind,
            index,
            span[0],
            span[1],
            'unsupported-or-invalid-record',
          );
          continue;
        }
        final record = {'kind': kind, 'sourceIndex': index, 'item': item};
        final size = utf8.encode(jsonEncode(record)).length + 1;
        // 512 exceeds the empty envelope, including the bounded part index.
        if (size + 512 > personalMigrationMaxBytes) {
          yield refusal(kind, index, span[0], span[1], 'oversized-record');
          continue;
        }
        if (records.isNotEmpty &&
            (records.length == personalMigrationMaxItems ||
                recordBytes + size + 512 > personalMigrationMaxBytes)) {
          await flush();
        }
        records.add(record);
        recordBytes += size;
      }
    }
    await flush();
    yield const [93];
  }

  final report = await write('unmigrated.json', reportChunks());
  final data = {
    'format': 'luobo-personal-migration',
    'version': 1,
    'applicationId': 'com.luoboduihua.imapp',
    'owner': null,
    'set': setId,
    'raw': raw.descriptor('original.json'),
    'report': report.descriptor('unmigrated.json'),
    'sourceCounts': sourceCounts,
    'rejected': rejected,
    'structuralErrors': structuralErrors,
    'parts': parts,
  };
  final bytes = utf8.encode(jsonEncode(data));
  final manifest = PersonalMigrationManifest.decode(bytes);
  final output = await write('manifest.json', Stream.value(bytes));
  if (output.bytes != bytes.length || output.sha256 != manifest.fingerprint) {
    _bad();
  }
  return manifest;
}
