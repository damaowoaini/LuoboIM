import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/contacts/presentation/select_contacts_page.dart';
import 'package:imapp/features/group/presentation/create_group_page.dart';

import 'group_creation_entry_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late GroupEntryFixture f;
  setUp(() => f = GroupEntryFixture());

  testWidgets('inbox selected friends reach accepted editor and one exact native group intent', (tester) async {
    try {
      await f.mount(tester, CreationSurface.inbox); await f.open(tester);
      await f.select(tester, 'bob'); await f.select(tester, 'carol'); await f.editor(tester);
      expect(f.creation.creates, isEmpty);
      expect(find.text('群成员（包含你共 3 人）'), findsOneWidget);
      await f.creation.tap(tester, '创建群聊');
      await f.until(tester, () => find.text('accepted-chat:created-cid').evaluate().isNotEmpty);
      expect(f.creation.creates, hasLength(1));
      expect(f.creation.creates.single['memberUserIDs'], ['bob', 'carol']);
      expect((f.creation.creates.single['groupInfo'] as Map)['groupType'], 2);
    } finally { await f.close(tester); }
  });

  testWidgets('single chat fixes peer plus implicit owner and adds only explicitly selected others', (tester) async {
    try {
      await f.mount(tester, CreationSurface.chat); await f.open(tester);
      final fixed = find.byKey(const ValueKey('fixed-group-member-bob'));
      expect(tester.widget<CheckboxListTile>(fixed).value, isTrue);
      expect(tester.widget<CheckboxListTile>(fixed).onChanged, isNull);
      expect(find.text('ID：alice'), findsOneWidget);
      expect(find.text('ID：bob（当前联系人）'), findsOneWidget);
      await f.select(tester, 'carol'); await f.editor(tester);
      expect(find.text('ID：bob'), findsOneWidget); expect(find.text('ID：carol'), findsOneWidget);
      await f.creation.tap(tester, '创建群聊');
      await f.until(tester, () => f.creation.creates.length == 1);
      expect(f.creation.creates.single['memberUserIDs'], ['bob', 'carol']);
      expect(f.verificationRequests, isEmpty);
    } finally { await f.close(tester); }
  });

  testWidgets('group-only inbox selection cannot silently open one-to-one chat', (tester) async {
    try {
      await f.mount(tester, CreationSurface.inbox); await f.open(tester);
      await f.select(tester, 'bob');
      expect(tester.widget<TextButton>(find.widgetWithText(TextButton, '确定 (1)')).onPressed, isNull);
      expect(f.creation.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
      expect(f.creation.creates, isEmpty);
    } finally { await f.close(tester); }
  });

  testWidgets('return from new editor retains fixed peer and additional selection for explicit editing', (tester) async {
    try {
      await f.mount(tester, CreationSurface.chat); await f.open(tester);
      await f.select(tester, 'carol'); await f.editor(tester);
      await f.creation.backFromCreation(tester);
      expect(find.byKey(const ValueKey('fixed-group-member-bob')), findsOneWidget);
      expect(find.text('确定 (2)'), findsOneWidget);
      await f.select(tester, 'carol');
      expect(tester.widget<TextButton>(find.widgetWithText(TextButton, '确定 (1)')).onPressed, isNull);
      expect(f.creation.creates, isEmpty);
    } finally { await f.close(tester); }
  });

  for (final multiple in [false, true]) {
    testWidgets('existing contacts single and multi selection remain available multiple=$multiple', (tester) async {
      try {
        await f.mount(tester, CreationSurface.contacts);
        await tester.tap(find.byTooltip('发起聊天')); await f.frame(tester);
        await f.until(tester, () => find.byType(CheckboxListTile).evaluate().length == 2);
        await tester.tap(find.byType(CheckboxListTile).at(0)); await tester.pump();
        if (multiple) { await tester.tap(find.byType(CheckboxListTile).at(1)); await tester.pump(); }
        await tester.tap(find.widgetWithText(TextButton, multiple ? '确定 (2)' : '确定 (1)'));
        await f.until(tester, () => multiple ? find.byType(CreateGroupPage).evaluate().isNotEmpty
          : find.text('accepted-chat:single-cid').evaluate().isNotEmpty);
        expect(f.creation.creates, isEmpty);
        expect(find.byType(CreateGroupPage), multiple ? findsOneWidget : findsNothing);
      } finally { await f.close(tester); }
    });
  }

  for (final status in [0, 2, 3]) {
    testWidgets('inbox preserves explicit verification preflight for status=$status without native create', (tester) async {
      try {
        f.verificationStatus = status; await f.mount(tester, CreationSurface.inbox);
        await tester.tap(f.entry);
        await f.until(tester, () => find.byType(SnackBar).evaluate().isNotEmpty);
        expect(find.byType(SelectContactsPage), findsNothing);
        expect(f.verificationRequests, hasLength(1)); expect(f.creation.creates, isEmpty);
      } finally { await f.close(tester); }
    });
  }

  testWidgets('malformed verification status is an error instead of approval', (tester) async {
    try {
      f.verificationStatus = '1'; await f.mount(tester, CreationSurface.inbox);
      await tester.tap(f.entry);
      await f.until(tester, () => find.text('暂时无法确认认证状态，请重试。').evaluate().isNotEmpty);
      expect(find.byType(SelectContactsPage), findsNothing); expect(f.creation.creates, isEmpty);
    } finally { await f.close(tester); }
  });

  testWidgets('failed status read releases admission and only explicit retry can continue', (tester) async {
    try {
      f.beforeVerification = () async => throw const AppFailure(FailureKind.network);
      await f.mount(tester, CreationSurface.inbox); await tester.tap(f.entry);
      await f.until(tester, () => find.text('暂时无法确认认证状态，请重试。').evaluate().isNotEmpty);
      expect(f.verificationRequests, hasLength(1)); expect(f.creation.creates, isEmpty);
      f.beforeVerification = null; await f.frame(tester);
      expect(f.verificationRequests, hasLength(1));
      await f.open(tester); expect(f.verificationRequests, hasLength(2));
    } finally { await f.close(tester); }
  });

  testWidgets('synchronous repeated inbox callback admits one read and no automatic create', (tester) async {
    final ready = f.deferred();
    try {
      f.beforeVerification = () => ready.future; await f.mount(tester, CreationSurface.inbox);
      final callback = tester.widget<IconButton>(
        find.widgetWithIcon(IconButton, Icons.group_add_outlined),
      ).onPressed!;
      callback(); callback();
      await f.until(tester, () => f.verificationRequests.length == 1);
      ready.complete();
      await f.until(tester, () => find.byType(SelectContactsPage).evaluate().isNotEmpty);
      expect(f.verificationRequests, hasLength(1)); expect(f.creation.creates, isEmpty);
    } finally { if (!ready.isCompleted) ready.complete(); await f.close(tester); }
  });

  for (final scope in ['owner', 'issuer', 'tenant']) {
    testWidgets('late verified reply cannot open selection after replacement $scope', (tester) async {
      final ready = f.deferred();
      try {
        f.beforeVerification = () => ready.future; await f.mount(tester, CreationSurface.inbox);
        await tester.tap(f.entry); await f.until(tester, () => f.verificationRequests.length == 1);
        await tester.runAsync(() => f.creation.base.signInOther(
          user: scope == 'owner' ? 'bob' : 'alice',
          issuer: scope == 'issuer' ? 'https://other.example.com/' : 'https://im.example.com/',
          tenant: scope == 'tenant' ? 'other-tenant' : 'fixture-tenant',
        ));
        ready.complete(); await f.frame(tester);
        expect(find.byType(SelectContactsPage), findsNothing); expect(f.creation.creates, isEmpty);
      } finally { if (!ready.isCompleted) ready.complete(); await f.close(tester); }
    });
  }

  testWidgets('foreign navigation permanently retires pending preflight but permits a new explicit click', (tester) async {
    final ready = f.deferred();
    try {
      f.beforeVerification = () => ready.future; await f.mount(tester, CreationSurface.inbox);
      await tester.tap(f.entry); await f.until(tester, () => f.verificationRequests.length == 1);
      await f.coverAndReturn(tester); ready.complete(); await f.frame(tester);
      expect(find.byType(SelectContactsPage), findsNothing);
      f.beforeVerification = null; await f.open(tester);
      expect(f.verificationRequests, hasLength(2)); expect(f.creation.creates, isEmpty);
    } finally { if (!ready.isCompleted) ready.complete(); await f.close(tester); }
  });

  testWidgets('removing entry route during preflight cannot reopen selection or feedback on blank page', (tester) async {
    final ready = f.deferred();
    try {
      f.beforeVerification = () => ready.future; await f.mount(tester, CreationSurface.inbox);
      await tester.tap(f.entry); await f.until(tester, () => f.verificationRequests.length == 1);
      f.creation.router!.go('/blank'); await f.frame(tester); ready.complete(); await f.frame(tester);
      expect(find.text('blank-route'), findsOneWidget);
      expect(find.byType(SelectContactsPage), findsNothing); expect(find.byType(SnackBar), findsNothing);
      expect(f.creation.creates, isEmpty);
    } finally { if (!ready.isCompleted) ready.complete(); await f.close(tester); }
  });

  testWidgets('old dedicated selector callback cannot acquire another account for its fixed peer', (tester) async {
    try {
      await f.mount(tester, CreationSurface.chat); await f.open(tester);
      await f.select(tester, 'carol');
      final callback = tester.widget<TextButton>(find.widgetWithText(TextButton, '确定 (2)')).onPressed!;
      await tester.runAsync(f.creation.replaceOwner); await f.frame(tester);
      callback(); await f.frame(tester);
      expect(find.byType(CreateGroupPage), findsNothing); expect(f.creation.creates, isEmpty);
      expect(find.text('会话已变化，请返回后重新选择好友'), findsOneWidget);
    } finally { await f.close(tester); }
  });

  for (final surface in [CreationSurface.inbox, CreationSurface.chat]) {
    testWidgets('old entry callback cannot acquire a fresh route turn after ABA surface=$surface', (tester) async {
      try {
        await f.mount(tester, surface);
        if (surface == CreationSurface.chat) await f.settings(tester);
        final VoidCallback callback = surface == CreationSurface.chat
          ? tester.widget<ListTile>(f.entry).onTap!
          : tester.widget<IconButton>(find.widgetWithIcon(IconButton, Icons.group_add_outlined)).onPressed!;
        await f.coverAndReturn(tester); callback(); await f.frame(tester);
        expect(find.byType(SelectContactsPage), findsNothing);
        expect(f.verificationRequests, isEmpty); expect(f.creation.creates, isEmpty);
        await tester.tap(f.entry);
        await f.until(tester, () => find.byType(SelectContactsPage).evaluate().isNotEmpty);
        expect(find.byType(SelectContactsPage), findsOneWidget);
      } finally { await f.close(tester); }
    });
  }

  testWidgets('old chat settings tile cannot start selection with replacement owner repositories', (tester) async {
    try {
      await f.mount(tester, CreationSurface.chat); await f.settings(tester);
      final callback = tester.widget<ListTile>(f.entry).onTap!;
      await tester.runAsync(f.creation.replaceOwner); await f.frame(tester);
      callback(); await f.frame(tester);
      expect(find.byType(SelectContactsPage), findsNothing);
      expect(f.creation.creates, isEmpty); expect(f.verificationRequests, isEmpty);
    } finally { await f.close(tester); }
  });

  testWidgets('old dedicated selector callback cannot survive route ABA while current explicit selection works', (tester) async {
    try {
      await f.mount(tester, CreationSurface.chat); await f.open(tester);
      await f.select(tester, 'carol');
      final callback = tester.widget<TextButton>(find.widgetWithText(TextButton, '确定 (2)')).onPressed!;
      await f.coverAndReturn(tester); callback(); await f.frame(tester);
      expect(find.byType(CreateGroupPage), findsNothing); expect(f.creation.creates, isEmpty);
      await f.editor(tester); expect(find.byType(CreateGroupPage), findsOneWidget);
    } finally { await f.close(tester); }
  });

  testWidgets('group chat settings does not masquerade as single-chat expansion', (tester) async {
    try {
      await f.mount(tester, CreationSurface.chat, group: true); await f.settings(tester);
      expect(find.widgetWithText(ListTile, '创建群聊'), findsNothing);
      expect(find.text('群资料与成员'), findsOneWidget); expect(f.creation.creates, isEmpty);
    } finally { await f.close(tester); }
  });

  for (final surface in [CreationSurface.inbox, CreationSurface.chat]) {
    testWidgets('small large-text group entry and stable selection remain usable surface=$surface', (tester) async {
      try {
        await f.mount(tester, surface, width: 320, height: 568); await f.open(tester);
        expect(find.text('ID：alice'), findsOneWidget);
        await f.select(tester, 'carol');
        if (surface == CreationSurface.inbox) await f.select(tester, 'bob');
        expect(find.widgetWithText(TextButton, '确定 (2)').hitTestable(), findsOneWidget);
        expect(tester.takeException(), isNull); expect(f.creation.creates, isEmpty);
      } finally { await f.close(tester); }
    });
  }

  testWidgets('unknown create through the new inbox entry remains frozen and cannot create twice', (tester) async {
    try {
      await f.mount(tester, CreationSurface.inbox); await f.open(tester);
      await f.select(tester, 'bob'); await f.select(tester, 'carol'); await f.editor(tester);
      f.creation.createReply = () async => throw const AppFailure(FailureKind.timeout);
      await f.creation.tap(tester, '创建群聊');
      await f.until(tester, () => find.textContaining('群创建结果未确认').evaluate().isNotEmpty);
      expect(f.creation.creates, hasLength(1));
      expect(find.widgetWithText(FilledButton, '创建群聊'), findsNothing);
      await f.frame(tester); expect(f.creation.creates, hasLength(1));
      expect(f.creation.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
    } finally { await f.close(tester); }
  });

  testWidgets('accepted create through chat expansion recovers only conversation read without another create', (tester) async {
    try {
      await f.mount(tester, CreationSurface.chat); await f.open(tester);
      await f.select(tester, 'carol'); await f.editor(tester);
      f.creation.conversationReply = () async => throw const AppFailure(FailureKind.network);
      await f.creation.tap(tester, '创建群聊');
      await f.until(tester, () => find.textContaining('群已创建，暂时无法打开会话').evaluate().isNotEmpty);
      expect(f.creation.creates, hasLength(1));
      f.creation.conversationReply = null; await f.creation.tap(tester, '重试打开会话');
      await f.until(tester, () => find.text('accepted-chat:created-cid').evaluate().isNotEmpty);
      expect(f.creation.creates, hasLength(1));
    } finally { await f.close(tester); }
  });
}
