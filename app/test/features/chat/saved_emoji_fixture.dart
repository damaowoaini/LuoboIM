import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/storage/object_uploader.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/data/chat_background_store.dart';
import 'package:imapp/features/chat/data/saved_emoji_store.dart';
import 'package:imapp/features/chat/domain/saved_emoji.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/features/chat/presentation/saved_emoji_sheet.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';
import 'chat_font_fixture.dart';

final class SavedEmojiFixture {
  final font = ChatFontFixture();
  SdkHarness get sdk => font.sdk;
  final values = <String, String>{};
  final releases = <void Function()>[];
  late SessionAccess access;
  late ChatRepository repository;
  late ChatController controller;
  late TenantApi api;
  late TestHttpAdapter metadata;
  late File original;
  late ChatBackgroundStore backgrounds;
  final uploads = <RequestOptions>[];
  final bodies = <List<int>>[];
  final finds = <Map>[];
  int writes = 0, faces = 0, uploadIds = 0;
  final uploadTypes = <String, String>{};
  bool started = false;
  bool failWrite = false,
      applyThenFail = false,
      failSend = false,
      sourceCurrent = true;
  Future<void> Function()? beforeWrite,
      beforeFind,
      beforeFace,
      beforeUpload,
      beforeComplete,
      beforeInitiate;
  Future<List<XFile>> Function()? picker;
  Uint8List? displayBytes;
  final png = base64Decode(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1sAAAAASUVORK5CYII=',
  );
  late final store = SavedEmojiStore(
    readString: (key) async => values[key],
    writeString: (key, body) async {
      writes++;
      await beforeWrite?.call();
      if (failWrite && !applyThenFail) {
        throw const FileSystemException('synthetic preferences failure');
      }
      values[key] = body;
      if (failWrite) {
        throw const FileSystemException('synthetic lost preferences reply');
      }
    },
  );
  SavedEmoji uploadEntry([String name = 'owned']) => SavedEmoji(
    url: Uri.parse('https://objects.example.com/$name.png'),
    width: 1,
    height: 1,
  );
  Map<String, dynamic> image({
    String id = 'source-image',
    bool private = false,
    int? expiry,
  }) => {
    ...SdkHarness.message(id: id, text: ''),
    'contentType': 102,
    'pictureElem': {
      'sourcePicture': {
        'url': 'https://objects.example.com/source.png',
        'width': 1,
        'height': 1,
      },
    },
    if (private || expiry != null)
      'attachedInfoElem': {
        'isPrivateChat': private,
        if (expiry != null) 'burnDuration': 10,
        if (expiry != null) 'hasReadTime': expiry - 10000,
      },
  };
  Completer<void> defer() {
    final c = Completer<void>();
    releases.add(() {
      if (!c.isCompleted) c.complete();
    });
    return c;
  }

  Future<void> start() async {
    if (started) {
      return;
    }
    started = true;
    await font.start();
    original = File('${sdk.directory.path}/saved-emoji-original.png');
    await original.writeAsBytes(png);
    sdk.messages.add(image());
    access = SessionAccess(sdk.session);
    metadata = TestHttpAdapter((options) async {
      if (options.path.endsWith('initiate_form_data')) {
        await beforeInitiate?.call();
        final id = 'emoji-upload-${++uploadIds}';
        uploadTypes[id] = options.data['contentType'] as String;
        return jsonResponse(
          jsonEncode({
            'errCode': 0,
            'data': {
              'url': 'https://objects.example.com/bucket',
              'id': id,
              'file': 'file',
              'formData': {'key': 'fixture'},
              'successCodes': [204],
            },
          }),
        );
      }
      await beforeComplete?.call();
      final id = options.data['id'] as String;
      final suffix = id == 'emoji-upload-1' ? '' : id.split('-').last;
      final extension = uploadTypes[id] == 'image/gif' ? 'gif' : 'png';
      return jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': {
            'url': 'https://objects.example.com/uploaded$suffix.$extension',
          },
        }),
      );
    });
    api = TenantApi(access, dio: Dio()..httpClientAdapter = metadata);
    sdk.overrideCall = (call) async {
      final args = call.arguments as Map;
      if (call.method == 'findMessageList') {
        finds.add(Map.from(args));
        await beforeFind?.call();
        return jsonEncode({
          'findResultItems': [
            for (final raw in args['searchParams'] as List)
              {
                'conversationID': raw['conversationID'],
                'messageList': [
                  for (final m in sdk.messages)
                    if ((raw['clientMsgIDList'] as List).contains(
                          m['clientMsgID'],
                        ) &&
                        (m['conversationID'] ?? 'single-alice-bob') ==
                            raw['conversationID'])
                      m,
                ],
              },
          ],
        });
      }
      if (call.method == 'createFaceMessage') {
        faces++;
        await beforeFace?.call();
        return jsonEncode({
          ...SdkHarness.message(
            id: 'face-$faces',
            text: '',
            sender: 'alice',
            status: 1,
          ),
          'contentType': 115,
          'faceElem': {'index': args['index'], 'data': args['data']},
        });
      }
      if (call.method == 'sendMessage' && failSend) {
        throw StateError('synthetic unknown send reply');
      }
      return sdk.respond(call);
    };
    repository = ChatRepository(
      gateway: sdk.gateway,
      session: sdk.session,
      conversationId: 'single-alice-bob',
    );
    controller = ChatController(repository);
    await controller.start();
    backgrounds = ChatBackgroundStore(
      supportDirectory: () async => sdk.directory,
    );
  }

  ObjectUploader uploader() => ObjectUploader(
    api,
    storage: Dio()..httpClientAdapter = _UploadAdapter(this),
  );
  Future<SavedEmojiSnapshot> seed([SavedEmoji? entry]) async {
    final old = await store.read(access, current: () => true);
    return store.add(access, old, entry ?? uploadEntry(), current: () => true);
  }

  Future<void> mount(
    WidgetTester tester, {
    bool chat = false,
    bool small = false,
  }) async {
    await tester.runAsync(start);
    if (small) {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
    }
    final theme = await captureTheme(tester);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => sdk.runtime),
          imGatewayProvider.overrideWithValue(sdk.gateway),
          tenantApiProvider.overrideWithValue(api),
          savedEmojiStoreProvider.overrideWithValue(store),
          chatReadingStoreProvider.overrideWithValue(font.store),
          chatBackgroundStoreProvider.overrideWithValue(backgrounds),
          savedEmojiPickerProvider.overrideWithValue(
            () => picker == null
                ? Future.value([XFile(original.path)])
                : picker!(),
          ),
          savedEmojiImageProvider.overrideWithValue(
            (_) => MemoryImage(displayBytes ?? png),
          ),
          savedEmojiUploaderProvider.overrideWithValue(
            (captured) => ObjectUploader(
              captured,
              storage: Dio()..httpClientAdapter = _UploadAdapter(this),
            ),
          ),
        ],
        child: MaterialApp(
          theme: theme,
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: TextScaler.linear(small ? 2 : 1)),
            child: child!,
          ),
          home: chat
              ? const ChatPage(conversationId: 'single-alice-bob')
              : Builder(
                  builder: (context) => Scaffold(
                    body: TextButton(
                      onPressed: () => showModalBottomSheet<void>(
                        context: context,
                        isScrollControlled: true,
                        builder: (_) => SavedEmojiSheet(
                          controller: controller,
                          sourceCurrent: () => sourceCurrent,
                          insertUnicode: (_) {},
                        ),
                      ),
                      child: const Text('打开表情面板'),
                    ),
                  ),
                ),
        ),
      ),
    );
    if (chat) {
      await font.until(
        tester,
        () => find.text('实际聊天文字用于字号验证').evaluate().length == 1,
      );
    } else {
      await tap(tester, find.text('打开表情面板'));
    }
  }

  Future<void> tap(WidgetTester tester, Finder target) async {
    await font.until(tester, () => target.evaluate().length == 1);
    await tester.ensureVisible(target);
    await font.frame(tester);
    await font.ready(tester, target);
    await tester.tap(target.hitTestable());
    await font.frame(tester);
  }

  Future<void> savedTab(WidgetTester tester) async {
    await tap(tester, find.text('常用表情'));
    await font.until(tester, () => find.byType(TabBar).evaluate().length == 1);
    final tabs = DefaultTabController.of(tester.element(find.byType(TabBar)));
    await font.until(tester, () => tabs.index == 1 && !tabs.indexIsChanging);
    await font.ready(tester, find.text('相册添加'));
    await font.until(
      tester,
      () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
    );
  }

  Future<void> choose(WidgetTester tester) async {
    await tap(tester, find.text('相册添加'));
    await font.until(
      tester,
      () => find.bySemanticsLabel('待保存的常用表情预览').evaluate().length == 1,
    );
    await font.frame(tester);
  }

  Future<T> actual<T>(WidgetTester tester, Future<T> Function() action) =>
      font.actual(tester, action);
  void release() {
    for (final release in releases) {
      release();
    }
  }

  Future<void> closeUnit() async {
    release();
    await store.flush();
    controller.dispose();
    api.close();
    backgrounds.dispose();
    store.dispose();
    await font.closeUnit();
  }

  Future<void> close(WidgetTester tester) async {
    release();
    try {
      debugPrintSynchronously('saved_emoji_cleanup drain.begin');
      await actual(
        tester,
        () => store.flush().timeout(const Duration(seconds: 20)),
      );
      debugPrintSynchronously('saved_emoji_cleanup drain.end');
      controller.dispose();
      api.close();
      debugPrintSynchronously('saved_emoji_cleanup sdk.begin');
      await actual(
        tester,
        () => sdk.dispose().timeout(const Duration(seconds: 20)),
      );
      debugPrintSynchronously('saved_emoji_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('saved_emoji_cleanup unmount.begin');
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pump();
      backgrounds.dispose();
      store.dispose();
      font.store.dispose();
      debugPrintSynchronously('saved_emoji_cleanup unmount.end');
    }
  }
}

class _UploadAdapter implements HttpClientAdapter {
  _UploadAdapter(this.f);
  final SavedEmojiFixture f;
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? stream,
    Future<void>? cancelFuture,
  ) async {
    f.uploads.add(options);
    f.bodies.add(await stream!.fold<List<int>>([], (a, b) => a..addAll(b)));
    await f.beforeUpload?.call();
    return ResponseBody.fromBytes([], 204);
  }

  @override
  void close({bool force = false}) {}
}

/// Two actual GIF image descriptors with different LZW pixels and loop timing.
/// Kept as fixture bytes; not generated by a live image service or old cache.
Uint8List twoFrameEmojiGif() => Uint8List.fromList([
  ...ascii.encode('GIF89a'),
  1,
  0,
  1,
  0,
  0x80,
  0,
  0,
  0,
  0,
  0,
  255,
  255,
  255,
  0x21,
  0xff,
  11,
  ...ascii.encode('NETSCAPE2.0'),
  3,
  1,
  0,
  0,
  0,
  for (final pixel in [0x44, 0x4c]) ...[
    0x21,
    0xf9,
    4,
    8,
    10,
    0,
    0,
    0,
    0x2c,
    0,
    0,
    0,
    0,
    1,
    0,
    1,
    0,
    0,
    2,
    2,
    pixel,
    1,
    0,
  ],
  0x3b,
]);
