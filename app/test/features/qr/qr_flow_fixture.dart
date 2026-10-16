import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/app.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/presentation/user_profile_page.dart';
import 'package:imapp/features/group/presentation/group_page.dart';
import 'package:imapp/features/mine/data/mine_repository.dart';
import 'package:imapp/features/qr/data/qr_platform.dart';
import 'package:imapp/features/qr/data/qr_repository.dart';
import 'package:imapp/features/qr/domain/qr_invite.dart';
import 'package:imapp/features/qr/presentation/qr_code_page.dart';
import 'package:imapp/features/qr/presentation/qr_scan_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import '../search/global_search_fixture.dart';

final class TestQrCamera implements QrCamera {
  void Function(List<String>)? listener;
  void Function()? previewError;
  int starts = 0, stops = 0, torches = 0, images = 0, disposals = 0;
  Future<List<String>> Function(String)? image;
  List<String>? initialCodes;
  @override
  Widget preview(void Function() onError) {
    previewError = onError;
    return const ColoredBox(color: Colors.black, child: SizedBox.expand());
  }
  @override
  Future<void> start(void Function(List<String>) callback) async {
    starts++; listener = callback;
    if (initialCodes != null) callback(initialCodes!);
  }
  @override
  Future<void> stop() async { stops++; listener = null; }
  @override
  Future<void> toggleTorch() async { torches++; }
  @override
  Future<List<String>> analyzeImage(String path) async { images++; return await image?.call(path) ?? const []; }
  @override
  Future<void> dispose() async { disposals++; listener = null; }
  void emit(String value) => listener?.call([value]);
}
final class TestQrPlatform implements QrPlatform {
  final camera = TestQrCamera();
  QrCameraPermission permission = QrCameraPermission.granted;
  int permissionCalls = 0, settingsCalls = 0, picks = 0, renders = 0, shares = 0, saves = 0, writes = 0;
  Future<QrCameraPermission> Function()? requestPermission;
  Future<XFile?> Function()? pick;
  Future<Uint8List> Function(String)? render;
  Future<QrExportResult> Function()? sharing;
  Future<bool> Function()? saveChoice;
  QrExportResult shareResult = QrExportResult.completed;
  bool saveSupported = false;
  @override QrCamera createCamera() => camera;
  @override Future<QrCameraPermission> cameraPermission() async { permissionCalls++; return await requestPermission?.call() ?? permission; }
  @override Future<bool> openCameraSettings() async { settingsCalls++; return true; }
  @override Future<XFile?> pickImage() async { picks++; return await pick?.call(); }
  @override Future<Uint8List> renderPng(String data) async { renders++; return await render?.call(data) ?? Uint8List.fromList([137, 80, 78, 71, 13, 10, 26, 10]); }
  @override Future<QrExportResult> sharePng(Uint8List bytes, Rect origin) async { shares++; return await sharing?.call() ?? shareResult; }
  @override bool get supportsSave => saveSupported;
  @override Future<QrExportResult> savePng(Uint8List bytes, bool Function() current) async {
    saves++;
    final selected = await saveChoice?.call() ?? true;
    if (!selected || !current()) return QrExportResult.cancelled;
    writes++; return QrExportResult.completed;
  }
}

final class QrFlowFixture {
  final business = GlobalFixture();
  final platform = TestQrPlatform();
  late QrRepository repository;
  GoRouter? router;
  final _sdkInFlight = <Future<void>>{};
  Future<void> start() async {
    await business.start();
    repository = QrRepository(business.repository.access, business.contacts,
      business.groupRepository, MineRepository(business.sdk.gateway, business.sdk.session.state.generation));
    business.sdk.overrideCall = (call) async {
      final reply = () async {
        final custom = business.pending?.call(call);
        if (custom != null) return await custom;
        if (call.method == 'getOneConversation') {
          final args = call.arguments as Map;
          return jsonEncode(SdkHarness.conversation(id: args['sessionType'] == 1 ? cidA : 'group-group-a',
            group: args['sessionType'] != 1));
        }
        return business.respond(call);
      }();
      // Observe completion only. The real MethodChannel reply/error still goes
      // unchanged to the production SDK/repository, never a success fallback.
      final observed = reply.then<void>((_) {}, onError: (Object _, StackTrace _) {});
      _sdkInFlight.add(observed);
      unawaited(observed.whenComplete(() => _sdkInFlight.remove(observed)));
      return await reply;
    };
  }
  Future<void> settle(WidgetTester tester) async {
    await tester.pump();
    var drained = false;
    await tester.runAsync(() async {
      for (var turn = 0; turn < 12; turn++) {
        final pending = _sdkInFlight.toList(growable: false);
        if (pending.isEmpty) { drained = true; return; }
        await Future.wait(pending).timeout(const Duration(seconds: 20));
      }
      throw StateError('QR fixture native reply chain did not finish');
    });
    if (!drained) throw StateError('QR fixture real async drain failed');
    // Meaningful pending IO is explicitly awaited above. A persistent UI
    // animation still fails; no observer/exception or product guard is removed.
    await tester.pumpAndSettle(const Duration(milliseconds: 100), EnginePhase.sendSemanticsUpdate, const Duration(seconds: 5));
  }
  void pause(WidgetTester tester) {
    final binding = tester.binding;
    if (binding.lifecycleState == null || binding.lifecycleState == AppLifecycleState.detached) {
      binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    }
    if (binding.lifecycleState == AppLifecycleState.resumed) binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    if (binding.lifecycleState == AppLifecycleState.inactive) binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    if (binding.lifecycleState == AppLifecycleState.hidden) binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
  }
  void resume(WidgetTester tester) {
    final binding = tester.binding;
    if (binding.lifecycleState == AppLifecycleState.paused) binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    if (binding.lifecycleState == AppLifecycleState.hidden) binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    if (binding.lifecycleState != AppLifecycleState.resumed) binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  }
  Future<void> tapVisible(WidgetTester tester, Finder finder) async {
    final scrollable = find.descendant(of: find.byType(ListView).last, matching: find.byType(Scrollable)).first;
    await tester.scrollUntilVisible(finder, 80, scrollable: scrollable);
    await tester.pump();
    await tester.tap(finder);
  }

  String code(QrInviteKind kind, String id) => QrInvite.encode(kind: kind, subjectId: id,
      credentials: business.sdk.session.state.credentials!);
  Future<void> mount(WidgetTester tester, {String initial = '/qr/scan', bool productionRouter = false,
      double width = 360, double height = 640}) async {
    await tester.runAsync(start);
    // The session boundary is genuinely signed in through SdkHarness's codec.
    // This host does not exercise HTTP authentication or the OS screen lock.
    business.sdk.runtime.phase = AppPhase.online;
    tester.view.physicalSize = Size(width, height); tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester);
    if (!productionRouter) {
      router = GoRouter(initialLocation: initial, routes: [
        GoRoute(path: '/qr/scan', builder: (_, _) => const QrScanPage()),
        GoRoute(path: '/qr/me', builder: (_, _) => const QrCodePage()),
        GoRoute(path: '/qr/group/:id', builder: (_, s) => QrCodePage(groupId: s.pathParameters['id'])),
        GoRoute(path: '/contacts/user/:id', builder: (_, s) => UserProfilePage(userId: s.pathParameters['id']!)),
        GoRoute(path: '/contacts/groups/:id', builder: (_, s) => GroupPage(groupId: s.pathParameters['id']!,
          joinSource: s.uri.queryParameters['joinSource'] == '4' ? 4 : 3)),
        // Destination observation only: real profile/repo/native navigation is
        // under test; ChatPage itself is covered by the existing source9.
        GoRoute(path: '/chat/:id', builder: (_, s) => Scaffold(body: Text('chat:${s.pathParameters['id']}'))),
      ]);
      business.router = router;
    }
    await tester.pumpWidget(ProviderScope(overrides: [
      imGatewayProvider.overrideWithValue(business.sdk.gateway),
      appRuntimeProvider.overrideWith((_) => business.sdk.runtime),
      tenantApiProvider.overrideWithValue(business.api),
      contactsRepositoryProvider.overrideWithValue(business.contacts),
      groupRepositoryProvider.overrideWithValue(business.groupRepository),
      qrPlatformProvider.overrideWithValue(platform),
    ], child: productionRouter ? _ProductionRouterHost(theme) : MaterialApp.router(routerConfig: router,
      theme: theme, builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
        .copyWith(textScaler: TextScaler.linear(1.6)), child: child!))));
    await settle(tester);
  }
  Future<void> coverAndReturn(WidgetTester tester) async {
    final nav = tester.state<NavigatorState>(find.byType(Navigator).last);
    unawaited(nav.push<void>(MaterialPageRoute(builder: (_) => const Scaffold(body: Text('foreign')))));
    await tester.pump(const Duration(milliseconds: 400));
    nav.pop(); await tester.pump(const Duration(milliseconds: 400));
  }
  Future<void> close(WidgetTester tester) async {
    // Retire capture/export intents without disposing the provider-owned
    // runtime in fakeAsync. The shared r3 cleanup then settles SDK before
    // unmount, preserving the real lifecycle and complete cleanup contract.
    pause(tester);
    await tester.pump();
    try { await business.close(tester); }
    finally { resume(tester); }
  }
}

class _ProductionRouterHost extends ConsumerWidget {
  const _ProductionRouterHost(this.theme);
  final ThemeData theme;
  @override Widget build(BuildContext context, WidgetRef ref) =>
    MaterialApp.router(routerConfig: ref.watch(routerProvider), theme: theme);
}
