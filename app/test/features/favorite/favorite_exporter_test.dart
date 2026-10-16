import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/favorite/data/favorite_exporter.dart';
import 'package:imapp/features/favorite/domain/favorite.dart';

import 'favorite_harness.dart';

void main() {
  late FavoriteHarness fixture;
  late Directory directory;
  setUp(() async {
    fixture = FavoriteHarness();
    fixture.items.addAll(
      List.generate(
        205,
        (index) => FavoriteHarness.item(
          '$index',
          content:
              '{"cloudCustomData":"keep-$index","textElem":{"content":"正文$index"}}',
        ),
      ),
    );
    await fixture.start();
    directory = await Directory.systemTemp.createTemp(
      'luobo-favorites-export-',
    );
  });
  tearDown(() async {
    await fixture.dispose();
    await directory.delete(recursive: true);
  });
  FavoriteExporter exporter() =>
      FavoriteExporter(fixture.repository, directory: () async => directory);
  test('a quarantined record on a later page rejects every export format and removes partial output', () async {
    fixture.items[101]['retentionBlocked'] = true;
    for (final format in FavoriteExportFormat.values) {
      await expectLater(
        exporter().export(format: format),
        throwsA(isA<AppFailure>()),
      );
      expect(
        await directory
            .list(recursive: true)
            .where((entry) => entry is File)
            .toList(),
        isEmpty,
      );
    }
  });
  test('statistics export records the owner and UTC period definition without reading message pages', () async {
    final exported = await exporter().exportStats();
    final document =
        jsonDecode(await File(exported.path).readAsString()) as Map;
    expect(document['format'], 'luobo-favorite-stats');
    expect(document['periodTimezone'], 'UTC');
    expect(document['weekStartsOn'], 'Monday');
    expect(document['accountKey'], fixture.api.access.credentials.accountKey);
    expect(document['stats']['totalCount'], 205);
    expect(document['stats']['categoriesWithCount'], hasLength(2));
    expect(
      fixture.http.requests.where(
        (request) => request.uri.path == '/api/favorite/list',
      ),
      isEmpty,
    );
  });
  test('export all reads every server page instead of the 30 loaded rows, and preserves wire content and categories', () async {
    expect((await fixture.repository.list()).items, hasLength(30));
    final file = await exporter().export();
    final raw = jsonDecode(await File(file.path).readAsString()) as Map;
    expect(file.count, 205);
    expect(raw['exportedCount'], 205);
    expect(raw['favorites'], hasLength(205));
    expect(raw['categories'], hasLength(2));
    expect(
      (raw['favorites'] as List).last['content'],
      fixture.items.last['content'],
    );
    final pages = fixture.http.requests
        .where((request) => request.uri.path == '/api/favorite/list')
        .toList();
    expect(pages.map((request) => request.data['pagination']['showNumber']), [
      30,
      100,
      100,
      100,
    ]);
    expect(pages.map((request) => request.data['pagination']['pageNumber']), [
      1,
      1,
      2,
      3,
    ]);
    expect(file.path, contains(fixture.api.access.credentials.accountKey));
  });
  test('filtered exports carry the requested category without confusing it with all categories', () async {
    fixture.items[0]['categoryID'] = 'work';
    fixture.items[204]['categoryID'] = 'work';
    final file = await exporter().export(
      query: const FavoriteQuery(categoryId: 'work'),
    );
    final raw = jsonDecode(await File(file.path).readAsString()) as Map;
    expect(raw['favorites'], hasLength(2));
    expect(raw['query']['categoryID'], 'work');
  });
  test('CSV protects formulas and quotes multiline values; TXT keeps original content', () async {
    fixture.items
      ..clear()
      ..add(
        FavoriteHarness.item(
          'formula',
          title: '=HYPERLINK("bad")',
          content: '一行,"二行"\n换行',
        ),
      );
    final csv = await exporter().export(format: FavoriteExportFormat.csv);
    final text = await File(csv.path).readAsString();
    expect(text, contains("'=HYPERLINK("));
    expect(text, contains('""二行""'));
    expect(text, contains('\n换行"'));
    final txt = await exporter().export(format: FavoriteExportFormat.txt);
    expect(await File(txt.path).readAsString(), contains('一行,"二行"\n换行'));
  });
  test('account invalidation during export deletes its partial file and cannot export the next account', () async {
    final started = Completer<void>();
    final response = Completer<Object?>();
    fixture.overrideRequest = (request) async {
      if (request.uri.path == '/api/favorite/list' &&
          request.data['pagination']['pageNumber'] == 2) {
        started.complete();
        return response.future;
      }
      return fixture.respond(request);
    };
    final exporting = exporter().export();
    final expected = expectLater(
      exporting,
      throwsA(
        isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.cancelled),
      ),
    );
    await started.future;
    await fixture.session.signOut();
    response.complete({'favorites': [], 'total': 205, 'hasMore': false});
    await expected;
    expect(
      await directory
          .list(recursive: true)
          .where((entry) => entry is File)
          .length,
      0,
    );
  });
  test('changed totals abort export rather than delivering a file falsely labeled complete', () async {
    fixture.overrideRequest = (request) async {
      if (request.uri.path == '/api/favorite/list' &&
          request.data['pagination']['pageNumber'] == 2) {
        fixture.items.add(FavoriteHarness.item('new'));
      }
      return fixture.respond(request);
    };
    await expectLater(
      exporter().export(),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.dataChanged,
        ),
      ),
    );
    expect(
      await directory
          .list(recursive: true)
          .where((entry) => entry is File)
          .length,
      0,
    );
  });
  test('a filesystem failure leaves the original path intact and never reports export success', () async {
    final occupied = File('${directory.path}/existing')
      ..writeAsStringSync('keep original');
    await expectLater(
      FavoriteExporter(
        fixture.repository,
        directory: () async => Directory(occupied.path),
      ).export(),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.localStorage,
        ),
      ),
    );
    expect(await occupied.readAsString(), 'keep original');
  });
}
