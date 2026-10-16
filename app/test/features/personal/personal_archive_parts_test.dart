import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/personal/data/personal_archive.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';

const _owner =
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

PersonalItem _item(
  int index, {
  PersonalKind kind = PersonalKind.note,
  String body = '',
}) => PersonalItem(
  id: 'item-$index',
  kind: kind,
  title: '资料 $index',
  body: body,
  createdAt: DateTime.utc(2026, 10, 1),
  updatedAt: DateTime.utc(2026, 10, 2),
  category: '本地备份',
  pinned: kind == PersonalKind.note,
  completed: kind == PersonalKind.todo,
  priority: 2,
  dueAt: kind == PersonalKind.todo ? DateTime.utc(2026, 11, 1) : null,
);

List<PersonalItem> _restore(List<List<int>> parts) => [
  for (final bytes in parts) ...PersonalArchive.decode(bytes).items,
];

void _expectSameContents(
  List<PersonalItem> actual,
  List<PersonalItem> expected,
) {
  final byKey = {
    for (final item in actual) '${item.kind.name}:${item.id}': item.toJson(),
  };
  expect(actual.length, expected.length);
  expect(byKey.length, expected.length);
  for (final item in expected) {
    expect(byKey['${item.kind.name}:${item.id}'], item.toJson());
  }
}

void main() {
  test('empty and small exports remain a single ordinary version1 archive', () {
    final empty = PersonalArchive.encodeParts(_owner, []);
    expect(empty, hasLength(1));
    expect(PersonalArchive.decode(empty.single).items, isEmpty);
    final items = [_item(1), _item(1, kind: PersonalKind.todo)];
    final parts = PersonalArchive.encodeParts(_owner, items);
    expect(parts, hasLength(1));
    final raw = jsonDecode(utf8.decode(parts.single)) as Map<String, dynamic>;
    expect(raw['format'], 'luobo-personal-data');
    expect(raw['version'], 1);
    expect(PersonalArchive.decode(parts.single).owner, _owner);
    _expectSameContents(_restore(parts), items);
  });

  test(
    '10000 records fit; 10001 restore completely from two independent files',
    () {
      final items = List.generate(10001, (index) => _item(index));
      expect(
        PersonalArchive.encodeParts(_owner, items.sublist(0, 10000)),
        hasLength(1),
      );
      final parts = PersonalArchive.encodeParts(_owner, items);
      expect(parts, hasLength(2));
      expect(parts.map((part) => PersonalArchive.decode(part).items.length), [
        10000,
        1,
      ]);
      _expectSameContents(_restore(parts), items);
    },
  );

  test(
    'UTF8 and escaped JSON limits produce only independently readable files',
    () {
      for (final body in ['界' * 1000000, '\n' * 1000000]) {
        final items = List.generate(9, (index) => _item(index, body: body));
        final parts = PersonalArchive.encodeParts(_owner, items);
        expect(parts.length, greaterThan(1));
        expect(
          parts.every((part) => part.length <= PersonalArchive.maxBytes),
          isTrue,
        );
        final restored = _restore(parts);
        expect(restored.length, items.length);
        expect(restored.map((item) => item.id).toSet().length, items.length);
        expect(restored.every((item) => item.body == body), isTrue);
        expect(
          parts.map((part) => PersonalArchive.decode(part).owner).toSet(),
          {_owner},
        );
      }
    },
  );

  test(
    'duplicate IDs are rejected across parts while note and todo IDs may match',
    () {
      final items = List.generate(10000, (index) => _item(index));
      expect(
        () => PersonalArchive.encodeParts(_owner, [...items, _item(0)]),
        throwsA(isA<AppFailure>()),
      );
      final valid = [...items, _item(0, kind: PersonalKind.todo)];
      _expectSameContents(
        _restore(PersonalArchive.encodeParts(_owner, valid)),
        valid,
      );
    },
  );

  test(
    'an invalid owner or item is rejected before any backup is returned',
    () {
      expect(
        () => PersonalArchive.encodeParts('other-owner', [_item(1)]),
        throwsA(isA<AppFailure>()),
      );
      expect(
        () => PersonalArchive.encodeParts(_owner, [
          _item(1).copyWith(title: 'x' * 1001),
        ]),
        throwsA(isA<AppFailure>()),
      );
    },
  );
}
