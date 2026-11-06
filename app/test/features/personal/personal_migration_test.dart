import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/personal/data/personal_archive.dart';
import 'package:imapp/features/personal/data/personal_migration.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';

void main() {
  final outputs = <String, List<int>>{};
  final completed = <String>[];
  setUp(() {
    outputs.clear();
    completed.clear();
  });
  Map<String, dynamic> note(int index, {String body = '正文'}) => {
    'id': 'old-$index',
    'title': '笔记$index',
    'content': body,
    'createdAt': '2025-01-01T00:00:00Z',
  };
  Future<PersonalMigrationFile> write(
    String name,
    Stream<List<int>> chunks,
  ) async {
    if (outputs.containsKey(name)) throw StateError('Backup exists');
    if (name != 'original.json') expect(completed.first, 'original.json');
    final builder = BytesBuilder(copy: false);
    await for (final chunk in chunks) {
      builder.add(chunk);
    }
    final bytes = builder.takeBytes();
    outputs[name] = bytes;
    completed.add(name);
    return PersonalMigrationFile(bytes.length, personalMigrationSha(bytes));
  }

  Future<PersonalMigrationManifest> export(
    String notes, {
    String todos = '[]',
  }) => exportLegacyPersonalMigration(
    write: write,
    rawNotes: notes,
    rawTodos: todos,
  );
  Future<List<Map<String, dynamic>>> records(
    PersonalMigrationManifest manifest,
  ) async {
    final values = <Map<String, dynamic>>[];
    for (var index = 0; index < manifest.parts.length; index++) {
      final spec = manifest.parts[index];
      final bytes = outputs[spec['file']]!;
      expect(bytes.length, lessThanOrEqualTo(personalMigrationMaxBytes));
      final part = manifest.decodePart(index, bytes);
      expect(part.length, lessThanOrEqualTo(personalMigrationMaxItems));
      values.addAll(part);
    }
    return values;
  }

  test(
    'legacy export partitions 10001 items and preserves IDs and source order',
    () async {
      expect(personalMigrationMaxBytes, PersonalArchive.maxBytes);
      expect(personalMigrationMaxItems, PersonalArchive.maxItems);
      final raw = jsonEncode(List.generate(10001, note));
      final manifest = await export(
        raw,
        todos: jsonEncode([
          {
            'id': 'old-0',
            'title': '同ID不同类型的待办',
            'description': '待办\n正文',
            'createdAt': '2025-01-01T00:00:00Z',
            'isCompleted': true,
            'priority': 2,
            'dueDate': '2027-01-01T00:00:00Z',
          },
        ]),
      );
      expect(manifest.parts.length, 2);
      expect(manifest.data['owner'], isNull);
      expect(manifest.rejected, 0);
      final values = await records(manifest);
      final notes = values.where((value) => value['kind'] == 'note');
      expect(
        notes.map((value) => value['sourceIndex']),
        orderedEquals(List.generate(10001, (i) => i)),
      );
      expect(
        notes.map((value) => (value['item'] as Map)['id']),
        orderedEquals(List.generate(10001, (i) => 'old-$i')),
      );
      final todo = PersonalItem.fromJson(
        PersonalKind.todo,
        values.singleWhere((value) => value['kind'] == 'todo')['item'],
      );
      expect(todo.id, 'old-0');
      expect(todo.completed, isTrue);
      expect(todo.priority, 2);
      expect(todo.dueAt, DateTime.utc(2027));
      expect(todo.body, '待办\n正文');
      expect(personalMigrationSha(outputs['original.json']!), manifest.setId);
      expect(completed.first, 'original.json');
      expect(completed.last, 'manifest.json');
      final backup = jsonDecode(utf8.decode(outputs['original.json']!)) as Map;
      expect(backup['local_notes'], raw);
      expect(
        () => manifest.parts.first['sha256'] = '0' * 64,
        throwsUnsupportedError,
      );
    },
  );

  test(
    'actual multibyte envelope stays bounded beyond the old 16MiB total',
    () async {
      final raw = jsonEncode(
        List.generate(6, (i) => note(i, body: '萝' * 1000000)),
      );
      expect(utf8.encode(raw).length, greaterThan(personalMigrationMaxBytes));
      final manifest = await export(raw);
      expect(manifest.parts.length, greaterThan(1));
      final values = await records(manifest);
      expect(values.length, 6);
      for (final value in values) {
        final item = PersonalItem.fromJson(PersonalKind.note, value['item']);
        expect(item.body, '萝' * 1000000);
      }
    },
  );

  test('oversized invalid and unknown-field items retain exact raw text without truncation', () async {
    final invalid = note(1, body: 'x' * 1000001);
    final unknown = note(2)
      ..['unverifiedLegacyField'] = 'must not silently drop';
    final raw = jsonEncode([note(0), invalid, unknown]);
    final manifest = await export(raw, todos: '[');
    expect(manifest.rejected, 2);
    expect(manifest.incomplete, isTrue);
    expect((await records(manifest)).map((e) => e['sourceIndex']), [0]);
    final backup = jsonDecode(utf8.decode(outputs['original.json']!)) as Map;
    expect(backup['local_notes'], raw);
    expect(backup['local_todos'], '[');
    final report = jsonDecode(utf8.decode(outputs['unmigrated.json']!)) as List;
    final spans = report.where((row) => row['sourceIndex'] != null).toList();
    expect(
      jsonDecode(
        raw.substring(
          spans[0]['utf16Start'] as int,
          spans[0]['utf16End'] as int,
        ),
      ),
      invalid,
    );
    expect(
      jsonDecode(
        raw.substring(
          spans[1]['utf16Start'] as int,
          spans[1]['utf16End'] as int,
        ),
      ),
      unknown,
    );
    // Raw exists before conversion; a second export never overwrites it.
    await expectLater(
      exportLegacyPersonalMigration(write: write, rawNotes: '[]'),
      throwsStateError,
    );
    expect(
      (jsonDecode(utf8.decode(outputs['original.json']!))
          as Map)['local_notes'],
      raw,
    );
  });

  test(
    'part/body source-index manifest and cross-account claims fail closed',
    () async {
      final manifest = await export(jsonEncode([note(0), note(1)]));
      final original = outputs['part-0.json']!;
      expect(
        () => manifest.decodePart(0, [...original, 32]),
        throwsFormatException,
      );
      final changedPart = jsonDecode(utf8.decode(original)) as Map;
      (changedPart['records'] as List)[1]['sourceIndex'] = 0;
      final changedBytes = utf8.encode(jsonEncode(changedPart));
      final changedManifest = jsonDecode(jsonEncode(manifest.data)) as Map;
      changedManifest['parts'][0]['bytes'] = changedBytes.length;
      changedManifest['parts'][0]['sha256'] = personalMigrationSha(
        changedBytes,
      );
      final rebound = PersonalMigrationManifest.decode(
        utf8.encode(jsonEncode(changedManifest)),
      );
      expect(() => rebound.decodePart(0, changedBytes), throwsFormatException);
      changedManifest['owner'] = 'a' * 64;
      expect(
        () => PersonalMigrationManifest.decode(
          utf8.encode(jsonEncode(changedManifest)),
        ),
        throwsFormatException,
      );
      changedManifest['owner'] = null;
      changedManifest['parts'][0]['file'] = '../other.json';
      expect(
        () => PersonalMigrationManifest.decode(
          utf8.encode(jsonEncode(changedManifest)),
        ),
        throwsFormatException,
      );
    },
  );
}
