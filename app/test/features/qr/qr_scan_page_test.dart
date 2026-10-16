import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/features/contacts/presentation/user_profile_page.dart';
import 'package:imapp/features/group/presentation/group_page.dart';
import 'package:imapp/features/qr/data/qr_platform.dart';
import 'package:imapp/features/qr/domain/qr_invite.dart';

import 'qr_flow_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('first QR delivered before camera start acknowledgement is not silently lost', (tester) async {
    final f = QrFlowFixture();
    try {
      await tester.runAsync(f.start);
      f.platform.camera.initialCodes = [f.code(QrInviteKind.user, 'bob')];
      await f.mount(tester);
      expect(find.text('确认个人资料'), findsOneWidget);
      expect(f.business.calls('getUsersInfo'), 1);
      await tester.tap(find.text('取消')); await f.settle(tester);
    } finally { await f.close(tester); }
  });
  testWidgets('camera torch and exact real user profile require explicit confirmation', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      expect(f.platform.permissionCalls, 1); expect(f.platform.camera.starts, 1);
      await tester.tap(find.text('手电筒')); await tester.pump();
      expect(f.platform.camera.torches, 1);
      f.platform.camera.emit(f.code(QrInviteKind.user, 'bob')); await f.settle(tester);
      expect(find.text('确认个人资料'), findsOneWidget);
      expect(find.text('ID：bob'), findsOneWidget);
      expect(find.byType(UserProfilePage), findsNothing);
      expect(f.business.calls('getUsersInfo'), greaterThan(0));
      expect(f.business.calls('addFriend'), 0); expect(f.business.calls('joinGroup'), 0);
      await tester.tap(find.text('查看资料')); await f.settle(tester);
      expect(find.byType(UserProfilePage), findsOneWidget);
      expect(f.router!.routerDelegate.currentConfiguration.uri.path, '/contacts/user/bob');
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('gallery legacy code exposes current service confirmation and cancel is read only', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.platform.pick = () async => XFile('/owned-selected-image.png');
      f.platform.camera.image = (_) async => ['com.jrtxa.wanfeng/addFriend/bob'];
      await tester.tap(find.text('从图片识别')); await f.settle(tester);
      expect(find.textContaining('旧版二维码'), findsOneWidget);
      expect(f.business.calls('getUsersInfo'), 0);
      await tester.tap(find.text('取消')); await f.settle(tester);
      expect(find.byType(UserProfilePage), findsNothing);
      expect(f.platform.camera.images, 1); expect(f.business.calls('addFriend'), 0);
    } finally { await f.close(tester); }
  });
  testWidgets('legacy service consent precedes real hydration and exact profile confirmation', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.platform.camera.emit('com.jrtxa.wanfeng/addFriend/bob'); await f.settle(tester);
      expect(f.business.calls('getUsersInfo'), 0);
      await tester.tap(find.text('在当前服务查找')); await f.settle(tester);
      expect(f.business.calls('getUsersInfo'), greaterThan(0));
      expect(find.text('确认个人资料'), findsOneWidget);
      await tester.tap(find.text('查看资料')); await f.settle(tester);
      expect(find.byType(UserProfilePage), findsOneWidget); expect(f.business.calls('addFriend'), 0);
    } finally { await f.close(tester); }
  });
  testWidgets('foreign service and arbitrary URL cause no profile or external navigation', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.platform.camera.emit('https://untrusted.example.com/'); await f.settle(tester);
      expect(find.textContaining('不是萝卜对话'), findsOneWidget);
      expect(f.business.calls('getUsersInfo'), 0);
      await tester.tap(find.text('开始扫描')); await f.settle(tester);
      final code = f.code(QrInviteKind.user, 'bob').replaceAll(RegExp(r'space=[0-9a-f]+'), 'space=${List.filled(64, '0').join()}');
      f.platform.camera.emit(code); await f.settle(tester);
      expect(find.textContaining('来自其他服务'), findsOneWidget);
      expect(f.business.calls('getUsersInfo'), 0);
    } finally { await f.close(tester); }
  });
  testWidgets('permission denial keeps gallery available and permanent denial offers settings retry', (tester) async {
    final f = QrFlowFixture(); f.platform.permission = QrCameraPermission.permanentlyDenied;
    try {
      await f.mount(tester);
      expect(f.platform.camera.starts, 0);
      expect(find.text('从图片识别'), findsOneWidget);
      await tester.tap(find.text('打开系统设置')); await f.settle(tester);
      expect(f.platform.settingsCalls, 1);
      f.platform.permission = QrCameraPermission.granted;
      await tester.tap(find.text('开始扫描')); await f.settle(tester);
      expect(f.platform.camera.starts, 1);
    } finally { await f.close(tester); }
  });
  testWidgets('gallery cancel and no code have explicit non-success states', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.platform.pick = () async => null;
      await tester.tap(find.text('从图片识别')); await f.settle(tester);
      expect(f.platform.camera.images, 0); expect(f.business.calls('getUsersInfo'), 0);
      f.platform.pick = () async => XFile('/owned-empty.png');
      await tester.tap(find.text('从图片识别')); await f.settle(tester);
      expect(find.text('没有识别到二维码'), findsOneWidget);
      expect(find.byType(UserProfilePage), findsNothing);
    } finally { await f.close(tester); }
  });
  testWidgets('owner switch while gallery waits cannot dispatch image decode or profile read', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      final pick = f.business.defer<XFile?>(null); f.platform.pick = () => pick.future;
      await tester.tap(find.text('从图片识别')); await tester.pump();
      await tester.runAsync(f.business.rotateOwner); await tester.pump();
      pick.complete(XFile('/old-account.png')); await f.settle(tester);
      expect(f.platform.camera.images, 0); expect(f.business.calls('getUsersInfo'), 0);
      expect(find.byType(AlertDialog), findsNothing);
    } finally { await f.close(tester); }
  });
  testWidgets('foreign-route ABA during image decode retires that intent but a new scan works', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      final image = f.business.defer<List<String>>(const []);
      f.platform.pick = () async => XFile('/owned-selected.png'); f.platform.camera.image = (_) => image.future;
      await tester.tap(find.text('从图片识别')); await tester.pump();
      expect(f.platform.camera.images, 1);
      await f.coverAndReturn(tester);
      image.complete([f.code(QrInviteKind.user, 'bob')]); await f.settle(tester);
      expect(f.business.calls('getUsersInfo'), 0); expect(find.byType(AlertDialog), findsNothing);
      await tester.tap(find.text('开始扫描')); await f.settle(tester);
      f.platform.camera.emit(f.code(QrInviteKind.user, 'bob')); await f.settle(tester);
      expect(find.text('确认个人资料'), findsOneWidget);
      await tester.tap(find.text('取消')); await f.settle(tester);
    } finally { await f.close(tester); }
  });
  testWidgets('old confirmation after a foreign overlay returns cannot open a profile', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.platform.camera.emit(f.code(QrInviteKind.user, 'bob')); await f.settle(tester);
      final confirm = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '查看资料')).onPressed!;
      await f.coverAndReturn(tester);
      confirm(); await tester.pump();
      expect(find.byType(UserProfilePage), findsNothing);
      await tester.tap(find.text('取消')); await f.settle(tester);
      expect(f.business.calls('addFriend'), 0);
    } finally { await f.close(tester); }
  });
  testWidgets('lifecycle pause stops camera and an old capture callback cannot navigate after resume', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester); final old = f.platform.camera.listener!;
      f.pause(tester); await tester.pump();
      f.resume(tester); await tester.pump();
      old([f.code(QrInviteKind.user, 'bob')]); await f.settle(tester);
      expect(f.platform.camera.stops, greaterThan(0)); expect(f.business.calls('getUsersInfo'), 0);
      await tester.tap(find.text('开始扫描')); await f.settle(tester);
      expect(f.platform.camera.starts, 2);
    } finally { await f.close(tester); }
  });
  testWidgets('group discovery reaches real group page and only explicit request sends joinSource4', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.business.pending = (call) => call.method == 'isJoinGroup' ? Future.value('false') : null;
      f.platform.camera.emit(f.code(QrInviteKind.group, 'group-a')); await f.settle(tester);
      expect(f.business.calls('joinGroup'), 0);
      await tester.tap(find.text('查看资料')); await f.settle(tester);
      expect(find.byType(GroupPage), findsOneWidget);
      expect(tester.widget<GroupPage>(find.byType(GroupPage)).joinSource, 4);
      await tester.tap(find.text('申请加入群聊')); await f.settle(tester);
      await tester.enterText(find.byType(TextField), '扫码核对后的申请');
      await tester.tap(find.text('确定')); await f.settle(tester);
      final calls = f.business.sdk.calls.where((c) => c.method == 'joinGroup').toList();
      expect(calls, hasLength(1));
      expect((calls.single.arguments as Map)['joinSource'], 4);
      expect((calls.single.arguments as Map)['groupID'], 'group-a');
    } finally { await f.close(tester); }
  });
  testWidgets('same-scope scan frames cannot duplicate an already resolving target', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      final response = f.business.defer<String>(jsonEncode([]));
      f.business.pending = (call) => call.method == 'getUsersInfo' ? response.future : null;
      final onCodes = f.platform.camera.listener!;
      onCodes([f.code(QrInviteKind.user, 'bob')]); await tester.pump();
      onCodes([f.code(QrInviteKind.user, 'bob')]); await tester.pump();
      expect(f.business.calls('getUsersInfo'), 1);
      response.complete(jsonEncode([{'publicInfo': {'userID': 'bob', 'nickname': '同名'}}]));
      await f.settle(tester);
      expect(find.text('确认个人资料'), findsOneWidget);
      await tester.tap(find.text('取消')); await f.settle(tester);
    } finally { await f.close(tester); }
  });
  testWidgets('small large-text scanner keeps errors and all available actions usable', (tester) async {
    final f = QrFlowFixture(); f.platform.permission = QrCameraPermission.denied;
    try {
      await f.mount(tester, width: 320, height: 568);
      expect(find.text('从图片识别').hitTestable(), findsOneWidget);
      expect(find.text('开始扫描').hitTestable(), findsOneWidget);
      expect(find.textContaining('需要相机权限'), findsOneWidget);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('failed authoritative hydration shows retry without opening a partial card', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.business.pending = (call) => call.method == 'getUsersInfo' ? Future.value('[]') : null;
      f.platform.camera.emit(f.code(QrInviteKind.user, 'bob')); await f.settle(tester);
      expect(find.textContaining('无法核验二维码'), findsOneWidget);
      expect(find.byType(UserProfilePage), findsNothing);
      expect(find.text('查看资料'), findsNothing);
      f.business.pending = null;
      await tester.tap(find.text('开始扫描')); await f.settle(tester);
      f.platform.camera.emit(f.code(QrInviteKind.user, 'bob')); await f.settle(tester);
      expect(find.text('确认个人资料'), findsOneWidget);
      await tester.tap(find.text('取消')); await f.settle(tester);
    } finally { await f.close(tester); }
  });
  testWidgets('image error releases busy and a later explicit selection may be decoded', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.platform.pick = () async => XFile('/owned-selected.png');
      f.platform.camera.image = (_) async => throw StateError('OS decoder unavailable');
      await tester.tap(find.text('从图片识别')); await f.settle(tester);
      expect(find.textContaining('图片识别失败'), findsOneWidget);
      f.platform.camera.image = (_) async => [f.code(QrInviteKind.user, 'bob')];
      await tester.tap(find.text('从图片识别')); await f.settle(tester);
      expect(find.text('确认个人资料'), findsOneWidget);
      await tester.tap(find.text('取消')); await f.settle(tester);
    } finally { await f.close(tester); }
  });
  testWidgets('leaving the scan page before gallery returns dispatches no decoder', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      final pick = f.business.defer<XFile?>(null); f.platform.pick = () => pick.future;
      await tester.tap(find.text('从图片识别')); await tester.pump();
      f.router!.go('/qr/me'); await tester.pump(const Duration(milliseconds: 400));
      pick.complete(XFile('/old-page.png')); await f.settle(tester);
      expect(f.platform.camera.images, 0); expect(f.platform.camera.disposals, 1);
      expect(find.text('确认个人资料'), findsNothing);
    } finally { await f.close(tester); }
  });
  testWidgets('late camera failure keeps explicit stop then restart available', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      expect(f.platform.camera.starts, 1);
      expect(find.text('重新扫描').hitTestable(), findsOneWidget);
      f.platform.camera.previewError!(); await f.settle(tester);
      expect(find.textContaining('相机暂不可用'), findsOneWidget);
      expect(find.text('开始扫描').hitTestable(), findsOneWidget);
      final stopped = f.platform.camera.stops;
      await tester.tap(find.text('开始扫描')); await f.settle(tester);
      expect(f.platform.camera.starts, 2);
      expect(f.platform.camera.stops, greaterThan(stopped));
      f.platform.camera.emit(f.code(QrInviteKind.user, 'bob')); await f.settle(tester);
      expect(find.text('确认个人资料'), findsOneWidget);
      await tester.tap(find.text('取消')); await f.settle(tester);
    } finally { await f.close(tester); }
  });
  testWidgets('logout while resolved confirmation is visible removes prior profile content', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.platform.camera.emit(f.code(QrInviteKind.user, 'bob')); await f.settle(tester);
      expect(find.text('ID：bob'), findsOneWidget);
      final oldConfirm = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '查看资料')).onPressed!;
      final reads = f.business.calls('getUsersInfo');
      await tester.runAsync(() => f.business.sdk.session.signOut(revoke: false));
      await f.settle(tester);
      expect(find.text('ID：bob'), findsNothing);
      expect(find.text('确认个人资料'), findsNothing);
      expect(find.text('本次扫描已失效'), findsOneWidget);
      oldConfirm(); await tester.pump();
      expect(find.byType(UserProfilePage), findsNothing);
      expect(f.business.calls('getUsersInfo'), reads);
      expect(f.business.calls('addFriend'), 0); expect(f.business.calls('joinGroup'), 0);
      await tester.tap(find.text('取消')); await f.settle(tester);
    } finally { await f.close(tester); }
  });
  testWidgets('issuer tenant replacement hides legacy dialog without popping a covering route', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester);
      f.platform.camera.emit('com.jrtxa.wanfeng/addFriend/bob'); await f.settle(tester);
      expect(find.text('ID：bob'), findsOneWidget);
      final oldConfirm = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '在当前服务查找')).onPressed!;
      final nav = tester.state<NavigatorState>(find.byType(Navigator).last);
      unawaited(nav.push<void>(MaterialPageRoute(builder: (_) => const Scaffold(body: Text('foreign-owned-page')))));
      await tester.pump(const Duration(milliseconds: 400));
      await tester.runAsync(f.business.rotateOwner); await f.settle(tester);
      expect(find.text('foreign-owned-page'), findsOneWidget);
      expect(find.text('ID：bob', skipOffstage: false), findsNothing);
      oldConfirm(); await tester.pump();
      expect(find.text('foreign-owned-page'), findsOneWidget);
      expect(f.business.calls('getUsersInfo'), 0); expect(f.business.calls('joinGroup'), 0);
      nav.pop(); await f.settle(tester);
      expect(find.text('本次扫描已失效'), findsOneWidget);
      expect(find.text('ID：bob'), findsNothing);
      await tester.tap(find.text('取消')); await f.settle(tester);
    } finally { await f.close(tester); }
  });

}
