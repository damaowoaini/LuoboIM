import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/storage/object_uploader.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';

import '../support/http_adapter.dart';
import 'privacy_bootstrap_test.dart' show StoreProbe, ImProbe;

class StorageProbe implements HttpClientAdapter {
  final requests = <RequestOptions>[];
  final bodies = <String>[];
  final entered = Completer<void>();
  final closed = Completer<void>();
  bool waitForCancel = false, responseClosed = false;
  int status = 204;
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(options);
    bodies.add(
      utf8.decode(
        await requestStream!.fold<List<int>>(
          [],
          (bytes, chunk) => bytes..addAll(chunk),
        ),
      ),
    );
    entered.complete();
    if (waitForCancel) {
      await cancelFuture;
      throw DioException(
        requestOptions: options,
        type: DioExceptionType.cancel,
      );
    }
    final body = StreamController<Uint8List>(
      onCancel: () {
        responseClosed = true;
        closed.complete();
      },
    );
    return ResponseBody(body.stream, status);
  }

  @override
  void close({bool force = false}) {}
}

void main() {
  late Directory directory;
  late File file;
  late SessionCoordinator session;
  late TenantApi api;
  late TestHttpAdapter metadata;
  late StorageProbe storage;
  late ObjectUploader uploader;
  late Map<String, Object?> signed;
  setUp(() async {
    directory = await Directory.systemTemp.createTemp('luobo-upload-test-');
    file = await File('${directory.path}/selected.txt')
        .writeAsString('media-bytes');
    session = SessionCoordinator(store: StoreProbe(), im: ImProbe());
    await session.signIn(
      (_) async => SessionCredentials(
        userId: 'alice',
        tenantId: 'tenant-a',
        tenantName: '服务',
        domains: ['im.example.com'],
        chatToken: 'chat-private',
        imToken: 'im-private',
      ),
    );
    signed = {
      'url': 'https://objects.example.com/bucket',
      'id': 'upload-1',
      'file': 'file',
      'formData': {'key': 'signed-key', 'policy': 'signed-policy'},
      'header': null,
      'successCodes': [204],
    };
    metadata = TestHttpAdapter(
      (options) async => jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': options.path.endsWith('initiate_form_data')
              ? signed
              : {'url': 'https://objects.example.com/published.txt'},
        }),
      ),
    );
    api = TenantApi(
      SessionAccess(session),
      dio: Dio()..httpClientAdapter = metadata,
    );
    storage = StorageProbe();
    uploader = ObjectUploader(api, storage: Dio()..httpClientAdapter = storage);
  });
  tearDown(() async {
    uploader.close();
    api.close();
    await session.dispose();
    await directory.delete(recursive: true);
  });

  test('signed multipart streams bytes without app tokens and completes only after provider success', () async {
    final result = await uploader.upload(
      file,
      contentType: 'text/plain',
      extension: '.txt',
    );
    expect(result.toString(), 'https://objects.example.com/published.txt');
    expect(metadata.requests.map((r) => r.uri.path), [
      '/api/object/initiate_form_data',
      '/api/object/complete_form_data',
    ]);
    expect(
      metadata.requests.every((r) => r.headers['token'] == 'im-private'),
      isTrue,
    );
    final provider = storage.requests.single;
    expect(
      provider.headers.keys.map((k) => k.toLowerCase()),
      isNot(contains('token')),
    );
    expect(
      provider.headers.keys.map((k) => k.toLowerCase()),
      isNot(contains('authorization')),
    );
    expect(provider.followRedirects, isFalse);
    expect(storage.bodies.single, contains('media-bytes'));
    expect(storage.bodies.single, contains('signed-policy'));
    expect(storage.bodies.single, isNot(contains(directory.path)));
    await storage.closed.future;
    expect(storage.responseClosed, isTrue);
    expect(
      metadata.requests.first.data['name'],
      matches(r'^alice/moments/[a-f0-9]{32}\.txt$'),
    );
  });

  test('unsafe storage targets and credential-carrying provider headers never upload', () async {
    for (final override in [
      {'url': 'http://objects.example.com/bucket'},
      {'url': 'https://user:pass@objects.example.com/bucket'},
      {
        'header': [
          {
            'key': 'Authorization',
            'values': ['secret'],
          },
        ],
      },
      {
        'header': [
          {
            'key': 'x-extra',
            'values': ['value\r\ninjected'],
          },
        ],
      },
    ]) {
      final original = Map<String, Object?>.from(signed);
      signed.addAll(override);
      await expectLater(
        uploader.upload(file, contentType: 'text/plain'),
        throwsA(isA<AppFailure>()),
      );
      signed = original;
    }
    expect(storage.requests, isEmpty);
    expect(
      metadata.requests.any((r) => r.path.endsWith('complete_form_data')),
      isFalse,
    );
  });

  test(
    'unexpected provider status closes response and never commits metadata',
    () async {
      storage.status = 201;
      await expectLater(
        uploader.upload(file, contentType: 'text/plain'),
        throwsA(isA<AppFailure>()),
      );
      await storage.closed.future;
      expect(storage.responseClosed, isTrue);
      expect(metadata.requests.length, 1);
    },
  );

  test(
    'account invalidation cancels an in-flight upload without completing it',
    () async {
      storage.waitForCancel = true;
      final pending = uploader.upload(file, contentType: 'text/plain');
      final expectation = expectLater(pending, throwsA(isA<AppFailure>()));
      await storage.entered.future;
      await session.signOut();
      await expectation;
      expect(metadata.requests.length, 1);
    },
  );

  test(
    'empty, oversized and missing selections do not request a signed form',
    () async {
      await file.writeAsBytes([]);
      await expectLater(
        uploader.upload(file, contentType: 'text/plain'),
        throwsA(isA<AppFailure>()),
      );
      final handle = await file.open(mode: FileMode.write);
      await handle.truncate(100 * 1024 * 1024 + 1);
      await handle.close();
      await expectLater(
        uploader.upload(file, contentType: 'text/plain'),
        throwsA(isA<AppFailure>()),
      );
      await file.delete();
      await expectLater(
        uploader.upload(file, contentType: 'text/plain'),
        throwsA(isA<AppFailure>()),
      );
      expect(metadata.requests, isEmpty);
    },
  );
}
