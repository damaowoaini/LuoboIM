import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/qr/presentation/qr_code_page.dart';
import 'package:imapp/features/qr/presentation/qr_scan_page.dart';
import 'package:imapp/features/contacts/presentation/user_profile_page.dart';
import 'package:imapp/features/group/presentation/group_page.dart';

import '../../core/im/sdk_harness.dart';
import 'qr_flow_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('actual production inbox and contacts scanner entries resolve registered QR routes', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, productionRouter: true);
      await tester.tap(find.byTooltip('扫一扫')); await tester.pumpAndSettle();
      expect(find.byType(QrScanPage), findsOneWidget);
      await tester.pageBack(); await tester.pumpAndSettle();
      await tester.tap(find.text('通讯录').last); await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('扫一扫')); await tester.pumpAndSettle();
      expect(find.byType(QrScanPage), findsOneWidget);
      expect(f.platform.camera.starts, 2);
    } finally { await f.close(tester); }
  });
  testWidgets('actual Mine entry reaches the registered personal QR display', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, productionRouter: true);
      await tester.tap(find.text('我的').last); await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('我的二维码'));
      await tester.tap(find.text('我的二维码')); await tester.pumpAndSettle();
      expect(find.byType(QrCodePage), findsOneWidget);
      expect(tester.widget<QrCodePage>(find.byType(QrCodePage)).groupId, isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('real group detail shows its QR entry only for active membership', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, initial: '/contacts/groups/group-a');
      await tester.ensureVisible(find.text('群二维码'));
      await tester.tap(find.text('群二维码')); await tester.pumpAndSettle();
      expect(tester.widget<QrCodePage>(find.byType(QrCodePage)).groupId, 'group-a');
    } finally { await f.close(tester); }
  });
  testWidgets('late user conversation read after foreign route ABA never pushes a chat', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, initial: '/contacts/user/bob');
      expect(find.byType(UserProfilePage), findsOneWidget);
      final response = f.business.defer<String>('{}');
      f.business.pending = (call) => call.method == 'getOneConversation' ? response.future : null;
      await tester.ensureVisible(find.text('发消息'));
      await tester.tap(find.text('发消息')); await tester.pump();
      expect(f.business.calls('getOneConversation'), 1);
      await f.coverAndReturn(tester);
      response.complete(jsonEncode(SdkHarness.conversation())); await tester.pumpAndSettle();
      expect(f.router!.routerDelegate.currentConfiguration.uri.path, '/contacts/user/bob');
      f.business.pending = null;
      await tester.tap(find.text('发消息')); await tester.pumpAndSettle();
      expect(find.text('chat:single-alice-bob'), findsOneWidget);
    } finally { await f.close(tester); }
  });
  testWidgets('late group conversation read cannot borrow a reentered group route', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, initial: '/contacts/groups/group-a');
      expect(find.byType(GroupPage), findsOneWidget);
      final response = f.business.defer<String>('{}');
      f.business.pending = (call) => call.method == 'getOneConversation' ? response.future : null;
      await tester.ensureVisible(find.text('进入群聊'));
      await tester.tap(find.text('进入群聊')); await tester.pump();
      expect(f.business.calls('getOneConversation'), 1);
      await f.coverAndReturn(tester);
      response.complete(jsonEncode(SdkHarness.conversation(id: 'group-group-a', group: true)));
      await tester.pumpAndSettle();
      expect(f.router!.routerDelegate.currentConfiguration.uri.path, '/contacts/groups/group-a');
      f.business.pending = null;
      await tester.tap(find.text('进入群聊')); await tester.pumpAndSettle();
      expect(find.text('chat:group-group-a'), findsOneWidget);
    } finally { await f.close(tester); }
  });
  testWidgets('QR group request confirmation covered and returned dispatches no join', (tester) async {
    final f = QrFlowFixture();
    try {
      await tester.runAsync(f.start);
      f.business.pending = (call) => call.method == 'isJoinGroup' ? Future.value('false') : null;
      await f.mount(tester, initial: '/contacts/groups/group-a?joinSource=4');
      await tester.tap(find.text('申请加入群聊')); await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), '原扫码请求');
      await f.coverAndReturn(tester);
      await tester.tap(find.text('确定')); await tester.pumpAndSettle();
      expect(f.business.calls('joinGroup'), 0);
    } finally { await f.close(tester); }
  });
}
