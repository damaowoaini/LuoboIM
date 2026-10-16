import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/storage/object_uploader.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/group/presentation/group_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';

/// Only OS selection, HTTP transport and native codec are replaced. GroupPage,
/// the image decoder/private stager, signed uploader and repository are real.
final class AvatarFixture {
  final sdk = SdkHarness();
  final navigator = GlobalKey<NavigatorState>();
  final body = ValueNotifier<String>('group-a');
  final providerRequests = <RequestOptions>[];
  final providerBodies = <List<int>>[];
  final sources = <ImageSource>[];
  final writes = <Map<String, dynamic>>[];
  int role = 100, status = 0;
  bool joined = true;
  Future<XFile?> Function(ImageSource)? picker;
  Future<void> Function(int)? providerReply;
  Future<void> Function()? metadataReply;
  Future<void> Function()? roleReply;
  late TenantApi api;
  late TestHttpAdapter metadata;
  late ProviderContainer container;
  late File original;
  int _upload = 0;

  Future<void> start() async {
    await sdk.start();
    original = File('${sdk.directory.path}/original.png');
    // Valid CRC-bearing 1x1 PNG; decoded by the actual ui.ImageDescriptor.
    await original.writeAsBytes(
      base64Decode(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jF9kAAAAASUVORK5CYII=',
      ),
    );
    metadata = TestHttpAdapter((options) async {
      final Object data;
      if (options.path.endsWith('initiate_form_data')) {
        data = {
          'url': 'https://objects.example.com/bucket',
          'id': 'upload-${++_upload}',
          'file': 'file',
          'formData': {'key': 'key-$_upload', 'policy': 'fixture'},
          'successCodes': [204],
        };
      } else if (options.path.endsWith('complete_form_data')) {
        data = {'url': 'https://objects.example.com/${options.data['id']}.png'};
      } else {
        data = {'users': []};
      }
      return jsonResponse(jsonEncode({'errCode': 0, 'data': data}));
    });
    api = TenantApi(
      SessionAccess(sdk.session),
      dio: Dio()..httpClientAdapter = metadata,
    );
    sdk.overrideCall = (call) async {
      final args = call.arguments as Map;
      switch (call.method) {
        case 'getGroupsInfo':
          return jsonEncode([
            for (final id in args['groupIDList'] as List)
              {
                'groupID': id,
                'groupName': '设计讨论群',
                'groupType': 2,
                'ownerUserID': 'alice',
                'memberCount': 3,
                'status': status,
              },
          ]);
        case 'isJoinGroup':
          return jsonEncode(joined);
        case 'getGroupMembersInfo':
          await roleReply?.call();
          return jsonEncode([
            {
              'groupID': args['groupID'],
              'userID': sdk.userId,
              'nickname': '我的昵称',
              'roleLevel': role,
              'muteEndTime': DateTime.now().millisecondsSinceEpoch + 600000,
            },
          ]);
        case 'setGroupInfo':
          writes.add(Map<String, dynamic>.from(args['groupInfo'] as Map));
          await metadataReply?.call();
          return null;
      }
      return sdk.respond(call);
    };
  }

  Future<void> mount(WidgetTester tester) async {
    await tester.runAsync(start);
    addTearDown(() async {
      api.close();
      await sdk.dispose();
    });
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imGatewayProvider.overrideWithValue(sdk.gateway),
          appRuntimeProvider.overrideWith((_) => sdk.runtime),
          tenantApiProvider.overrideWithValue(api),
          groupAvatarPickerProvider.overrideWithValue((source) async {
            sources.add(source);
            return picker == null ? XFile(original.path) : picker!(source);
          }),
          groupAvatarUploaderProvider.overrideWithValue(
            (capturedApi) => ObjectUploader(
              capturedApi,
              storage: Dio()..httpClientAdapter = _ProviderAdapter(this),
            ),
          ),
        ],
        child: Builder(
          builder: (context) {
            container = ProviderScope.containerOf(context, listen: false);
            return MaterialApp(
              navigatorKey: navigator,
              theme: theme,
              builder: (context, child) => MediaQuery(
                data: MediaQuery.of(context)
                    .copyWith(textScaler: TextScaler.linear(1.6)),
                child: child!,
              ),
              home: ValueListenableBuilder<String>(
                valueListenable: body,
                builder: (_, id, _) => id.isEmpty
                    ? const SizedBox.shrink()
                    : GroupPage(groupId: id),
              ),
            );
          },
        ),
      ),
    );
    await routeFrame(tester);
  }

  Finder get edit => find.byKey(const ValueKey('group-avatar-edit'));
  Future<void> select(WidgetTester tester, {bool camera = false}) async {
    expect(edit, findsOneWidget);
    await tester.tap(edit);
    await routeFrame(tester);
    await tester.tap(find.text(camera ? '拍照' : '从相册选择'));
    await tester.pump();
  }

  Future<void> preview(WidgetTester tester) async {
    await select(tester);
    await until(tester, () => find.text('使用这张群头像？').evaluate().isNotEmpty);
  }

  Future<void> approve(WidgetTester tester) async {
    await preview(tester);
    await tester.tap(find.widgetWithText(FilledButton, '使用'));
    await tester.pump();
  }

  Future<void> signInOther({
    String user = 'bob',
    String tenant = 'fixture-tenant',
    String issuer = 'https://im.example.com/',
  }) async {
    await sdk.session.signOut(revoke: false);
    sdk.userId = user;
    await sdk.session.signIn(
      (_) async => SessionCredentials(
        userId: user,
        tenantId: tenant,
        tenantName: '测试租户',
        domains: ['im.example.com'],
        issuer: issuer,
        chatToken: 'other-chat',
        imToken: 'other-im',
      ),
    );
  }

  Future<void> close(WidgetTester tester) async {
    // Match the established contacts-pages fixture order. Dispose the real
    // runtime while runAsync owns its I/O; unmounting first starts its shutdown
    // in the fake widget zone and then waiting in runAsync cannot advance it.
    await tester.runAsync(() async {
      api.close();
      await sdk.dispose();
    });
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
    body.dispose();
  }
}

final class _ProviderAdapter implements HttpClientAdapter {
  _ProviderAdapter(this.fixture);
  final AvatarFixture fixture;
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? stream,
    Future<void>? cancelFuture,
  ) async {
    fixture.providerRequests.add(options);
    final index = fixture.providerRequests.length;
    fixture.providerBodies.add(
      await stream!.fold<List<int>>([], (a, b) => a..addAll(b)),
    );
    await fixture.providerReply?.call(index);
    return ResponseBody.fromBytes([], 204);
  }

  @override
  void close({bool force = false}) {}
}

/// Bounded condition polling only yields OS file/codec work; it never treats a
/// fixed delay as evidence that a stage finished. The assertion names the gate.
Future<void> until(WidgetTester tester, bool Function() condition) async {
  for (var i = 0; i < 150; i++) {
    await tester.runAsync(() async => Future<void>.delayed(Duration.zero));
    await tester.pump(const Duration(milliseconds: 16));
    if (condition()) return;
  }
  throw TestFailure('Expected avatar stage did not complete within 150 pumps');
}

// Only advance route animations. A legitimate indeterminate progress indicator
// keeps scheduling frames, so pumpAndSettle cannot represent operation completion.
Future<void> routeFrame(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 400));
  await tester.pump();
}
