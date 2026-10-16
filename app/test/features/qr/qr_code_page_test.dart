import 'dart:typed_data';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:imapp/features/qr/data/qr_platform.dart';
import 'package:imapp/features/qr/domain/qr_invite.dart';
import 'package:imapp/features/qr/presentation/qr_code_page.dart';
import 'package:qr_flutter/qr_flutter.dart';

import 'qr_flow_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('personal code renders the authoritative exact ID with no credential payload', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, initial: '/qr/me');
      final image = tester.widget<QrCodeImage>(find.byType(QrCodeImage));
      final invite = QrInvite.decode(image.data, f.repository.access.credentials);
      expect(invite.subjectId, 'alice'); expect(invite.kind, QrInviteKind.user);
      expect(image.data, isNot(contains('fixture-chat'))); expect(image.data, isNot(contains('fixture-im')));
      expect(find.text('ID：alice'), findsOneWidget);
      expect(find.text('保存图片'), findsNothing);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('real dismissed-group callback withdraws QR and blocks an already rendering share', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, initial: '/qr/group/group-a');
      final render = f.business.defer<Uint8List>(Uint8List(0)); f.platform.render = (_) => render.future;
      await f.tapVisible(tester, find.text('分享二维码')); await tester.pump();
      f.business.groups[0]['status'] = 2;
      await tester.runAsync(() => f.business.sdk.callback(ListenerType.groupListener, {
        'type': 'onGroupDismissed', 'data': jsonEncode(f.business.groups[0]),
      }));
      await tester.pump(); await tester.pump();
      render.complete(Uint8List(8)); await f.settle(tester);
      expect(find.byType(QrImageView), findsNothing); expect(f.platform.shares, 0);
      expect(find.textContaining('无法核验群资料'), findsOneWidget);
    } finally { await f.close(tester); }
  });
  testWidgets('current ordinary group member sees a full QR and can explicitly share PNG', (tester) async {
    final f = QrFlowFixture();
    try {
      await tester.runAsync(f.start);
      f.business.pending = (call) => call.method == 'getGroupMembersInfo' ? Future.value(jsonEncode([
        {'groupID': 'group-a', 'userID': 'alice', 'nickname': 'alice', 'roleLevel': 20},
      ])) : null;
      await f.mount(tester, initial: '/qr/group/group-a');
      expect(find.byType(QrImageView), findsOneWidget);
      await f.tapVisible(tester, find.text('分享二维码')); await f.settle(tester);
      expect(f.platform.renders, 1); expect(f.platform.shares, 1);
      expect(find.text('二维码已分享'), findsOneWidget);
    } finally { await f.close(tester); }
  });
  testWidgets('dismissed or nonmember group display fails closed with an explicit retry', (tester) async {
    final f = QrFlowFixture();
    try {
      await tester.runAsync(f.start);
      f.business.pending = (call) => call.method == 'isJoinGroup' ? Future.value('false') : null;
      await f.mount(tester, initial: '/qr/group/group-a');
      expect(find.byType(QrImageView), findsNothing);
      expect(find.textContaining('无法核验群资料'), findsOneWidget);
      expect(find.text('重试'), findsOneWidget);
      expect(f.platform.shares, 0);
      final deniedReads = f.business.calls('getGroupsInfo');
      await tester.pump(const Duration(seconds: 10)); await f.settle(tester);
      expect(f.business.calls('getGroupsInfo'), deniedReads);
      // A later explicit retry may observe new authoritative membership.
      f.business.pending = null;
      await tester.tap(find.text('重试')); await f.settle(tester);
      expect(f.business.calls('getGroupsInfo'), greaterThan(deniedReads));
      expect(find.byType(QrImageView), findsOneWidget);
    } finally { await f.close(tester); }
  });
  testWidgets('unavailable share is not reported as saved or shared and explicit retry works', (tester) async {
    final f = QrFlowFixture(); f.platform.shareResult = QrExportResult.unavailable;
    try {
      await f.mount(tester, initial: '/qr/me');
      await f.tapVisible(tester, find.text('分享二维码')); await f.settle(tester);
      expect(find.textContaining('无法分享图片'), findsOneWidget);
      expect(find.text('二维码已分享'), findsNothing);
      f.platform.shareResult = QrExportResult.completed;
      await f.tapVisible(tester, find.text('分享二维码')); await f.settle(tester);
      expect(f.platform.shares, 2); expect(find.text('二维码已分享'), findsOneWidget);
    } finally { await f.close(tester); }
  });
  testWidgets('synchronous repeated export tap dispatches only one render and share', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, initial: '/qr/me');
      final render = f.business.defer<Uint8List>(Uint8List(0));
      f.platform.render = (_) => render.future;
      final action = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '分享二维码')).onPressed!;
      action(); action(); await tester.pump();
      expect(f.platform.renders, 1);
      render.complete(Uint8List.fromList([137, 80, 78, 71])); await f.settle(tester);
      expect(f.platform.shares, 1);
    } finally { await f.close(tester); }
  });
  testWidgets('render completing after owner issuer and tenant switch never starts OS sharing', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, initial: '/qr/me');
      final render = f.business.defer<Uint8List>(Uint8List(0)); f.platform.render = (_) => render.future;
      await f.tapVisible(tester, find.text('分享二维码')); await tester.pump();
      await tester.runAsync(f.business.rotateOwner); await tester.pump();
      render.complete(Uint8List(8)); await f.settle(tester);
      expect(f.platform.shares, 0); expect(find.text('二维码已分享'), findsNothing);
      expect(find.byType(QrImageView), findsNothing);
    } finally { await f.close(tester); }
  });
  testWidgets('route ABA during render cannot share and later explicit export is usable', (tester) async {
    final f = QrFlowFixture();
    try {
      await f.mount(tester, initial: '/qr/me');
      final render = f.business.defer<Uint8List>(Uint8List(0)); f.platform.render = (_) => render.future;
      await f.tapVisible(tester, find.text('分享二维码')); await tester.pump();
      await f.coverAndReturn(tester);
      render.complete(Uint8List(8)); await f.settle(tester);
      expect(f.platform.shares, 0);
      f.platform.render = null;
      await f.tapVisible(tester, find.text('分享二维码')); await f.settle(tester);
      expect(f.platform.shares, 1);
    } finally { await f.close(tester); }
  });
  testWidgets('supported save rechecks the captured owner after OS destination selection', (tester) async {
    final f = QrFlowFixture(); f.platform.saveSupported = true;
    try {
      await f.mount(tester, initial: '/qr/me');
      final choice = f.business.defer<bool>(false); f.platform.saveChoice = () => choice.future;
      await f.tapVisible(tester, find.text('保存图片')); await tester.pump();
      expect(f.platform.saves, 1);
      await tester.runAsync(f.business.rotateOwner); await tester.pump();
      choice.complete(true); await f.settle(tester);
      expect(f.platform.writes, 0); expect(find.text('二维码已保存'), findsNothing);
    } finally { await f.close(tester); }
  });
  testWidgets('supported save confirms only completed file writes while cancel remains silent', (tester) async {
    final f = QrFlowFixture(); f.platform.saveSupported = true;
    try {
      await f.mount(tester, initial: '/qr/me');
      f.platform.saveChoice = () async => false;
      await f.tapVisible(tester, find.text('保存图片')); await f.settle(tester);
      expect(f.platform.writes, 0); expect(find.text('二维码已保存'), findsNothing);
      f.platform.saveChoice = () async => true;
      await f.tapVisible(tester, find.text('保存图片')); await f.settle(tester);
      expect(f.platform.writes, 1); expect(find.text('二维码已保存'), findsOneWidget);
    } finally { await f.close(tester); }
  });
}
