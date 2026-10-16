import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/personal/data/personal_archive.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';

void main() {
  test(
    'single-file export refuses a record count its own importer cannot restore',
    () {
      final time = DateTime.utc(2026, 10, 1);
      final items = List.generate(
        10001,
        (index) => PersonalItem(
          id: 'backup-$index',
          kind: PersonalKind.note,
          title: '笔记 $index',
          body: '',
          createdAt: time,
          updatedAt: time,
        ),
      );
      expect(
        () => PersonalArchive.encode(List.filled(64, 'a').join(), items),
        throwsA(isA<AppFailure>()),
      );
    },
  );
}
