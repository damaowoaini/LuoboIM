import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/favorite/data/favorite_attachment_store.dart';

import '../../support/http_adapter.dart';
import 'favorite_harness.dart';

void main() {
  late FavoriteHarness fixture;
  late Directory root;
  late FavoriteAttachmentStore store;
  late TestHttpAdapter adapter;
  setUp(() async {
    fixture = FavoriteHarness();
    await fixture.start();
    root = await Directory.systemTemp.createTemp('luobo-favorite-download-');
  });
  tearDown(() async {
    store.dispose();
    await fixture.dispose();
    await root.delete(recursive: true);
  });
  void create(FutureOr<ResponseBody> Function(RequestOptions) response) {
    adapter = TestHttpAdapter((request) async => response(request));
    store = FavoriteAttachmentStore(
      fixture.api.access,
      dio: Dio()..httpClientAdapter = adapter,
      directory: () async => root,
    );
  }

  Future<List<File>> files() async => root
      .list(recursive: true)
      .where((entry) => entry is File)
      .cast<File>()
      .toList();

  test('cancelling after the first chunk removes the file and never returns partial content', () async {
    create(
      (_) => ResponseBody(
        Stream.fromIterable([
          Uint8List.fromList([1, 2]),
          Uint8List.fromList([3, 4]),
        ]),
        200,
      ),
    );
    await expectLater(
      store.download(
        Uri.parse('https://files.example.test/a'),
        name: 'a.pdf',
        progress: (_, _) => store.cancel(),
      ),
      throwsA(
        isA<AppFailure>().having(
          (error) => error.kind,
          'kind',
          FailureKind.cancelled,
        ),
      ),
    );
    expect(await files(), isEmpty);
  });
  test('streams every byte to a safe account-owned file without sending either service token', () async {
    create(
      (_) => ResponseBody(
        Stream.fromIterable([
          Uint8List.fromList([1, 2, 3]),
          Uint8List.fromList([4, 5]),
        ]),
        200,
        headers: {
          'content-length': ['5'],
        },
      ),
    );
    final progress = <int>[];
    final file = await store.download(
      Uri.parse('https://files.example.test/signed/document'),
      name: '../../private/资料.pdf',
      progress: (received, _) => progress.add(received),
    );
    expect(await file.readAsBytes(), [1, 2, 3, 4, 5]);
    expect(file.path, contains(fixture.api.access.credentials.accountKey));
    expect(file.uri.pathSegments.last, '资料.pdf');
    expect(progress, [3, 5]);
    expect(
      adapter.requests.single.headers.keys.map((key) => key.toLowerCase()),
      isNot(contains('token')),
    );
    expect(
      adapter.requests.single.headers.keys.map((key) => key.toLowerCase()),
      isNot(contains('authorization')),
    );
    expect(safeFavoriteFilename('..'), '收藏附件.bin');
    expect(safeFavoriteFilename('C:\\folder\\a\x00.pdf'), 'a.pdf');
  });
  test('redirects are revalidated and a downgrade or embedded credentials never reach the network', () async {
    create(
      (_) => ResponseBody.fromString(
        '',
        302,
        headers: {
          'location': ['http://unsafe.example.test/file'],
        },
      ),
    );
    await expectLater(
      store.download(Uri.parse('https://files.example.test/a'), name: 'a.pdf'),
      throwsA(isA<AppFailure>()),
    );
    expect(adapter.requests, hasLength(1));
    await expectLater(
      store.download(
        Uri.parse('https://user:password@files.example.test/a'),
        name: 'a.pdf',
      ),
      throwsA(isA<AppFailure>()),
    );
    expect(adapter.requests, hasLength(1));
    expect(await files(), isEmpty);
  });
  test('truncated bodies remove partial files instead of reporting a completed download', () async {
    create(
      (_) => ResponseBody.fromBytes(
        [1, 2],
        200,
        headers: {
          'content-length': ['4'],
        },
      ),
    );
    await expectLater(
      store.download(Uri.parse('https://files.example.test/a'), name: 'a.pdf'),
      throwsA(
        isA<AppFailure>().having(
          (error) => error.kind,
          'kind',
          FailureKind.invalidResponse,
        ),
      ),
    );
    expect(await files(), isEmpty);
  });
  test('a source retention denial after the first chunk removes the generated attachment and forbids later bytes', () async {
    var allowed = true;
    create(
      (_) => ResponseBody(
        Stream.fromIterable([
          Uint8List.fromList([1, 2]),
          Uint8List.fromList([3, 4]),
        ]),
        200,
      ),
    );
    await expectLater(
      store.download(
        Uri.parse('https://files.example.test/a'),
        name: 'a.pdf',
        checkSource: () {
          if (!allowed) throw const AppFailure(FailureKind.permissionDenied);
        },
        progress: (_, _) => allowed = false,
      ),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.permissionDenied,
        ),
      ),
    );
    expect(await files(), isEmpty);
  });
  test('logout during a slow response cancels the old download and cannot write into the next account', () async {
    final started = Completer<void>();
    final response = Completer<ResponseBody>();
    create((_) {
      started.complete();
      return response.future;
    });
    final download = store.download(
      Uri.parse('https://files.example.test/a'),
      name: 'a.pdf',
    );
    final result = expectLater(
      download,
      throwsA(
        isA<AppFailure>().having(
          (error) => error.kind,
          'kind',
          FailureKind.cancelled,
        ),
      ),
    );
    await started.future;
    await fixture.session.signOut();
    await fixture.session.signIn((_) async => fixture.credentials('bob'));
    response.complete(ResponseBody.fromBytes([1, 2], 200));
    await result;
    expect(await files(), isEmpty);
    await expectLater(
      store.download(
        Uri.parse('https://files.example.test/new'),
        name: 'new.pdf',
      ),
      throwsA(isA<AppFailure>()),
    );
    expect(adapter.requests, hasLength(1));
  });
}
