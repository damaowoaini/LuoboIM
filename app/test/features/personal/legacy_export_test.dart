import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/personal/data/personal_archive.dart';

import '../../../../migration/legacy_personal_export/legacy_personal_export.dart';

void main() {
  test('legacy helper produces an unowned archive accepted by the actual importer', () {
    const notes =
        '[{"id":"old-id","title":"原笔记","content":"原文","createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-01T10:00:00Z","isPinned":true}]';
    final bytes = legacyPersonalBackupBytes(rawNotes: notes);
    final archive = PersonalArchive.decode(bytes);
    expect(archive.owner, isNull);
    expect(archive.items.single.title, '原笔记');
    expect(archive.items.single.pinned, isTrue);
    expect((jsonDecode(utf8.decode(bytes)) as Map)['local_notes'], notes);
  });
  test(
    'corrupt legacy strings remain recoverable but cannot partially import',
    () {
      const corrupt = '[{"broken"';
      final bytes = legacyPersonalBackupBytes(rawNotes: corrupt);
      expect((jsonDecode(utf8.decode(bytes)) as Map)['local_notes'], corrupt);
      expect(() => PersonalArchive.decode(bytes), throwsA(isA<AppFailure>()));
    },
  );
}
