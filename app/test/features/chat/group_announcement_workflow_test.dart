import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/group_announcement_panel.dart';

import 'group_announcement_fixture.dart';

void main() {
  testWidgets(
    'actual group chat opens a fresh ordinary-member announcement and its own detail stays readable',
    (tester) async {
      final f = GroupAnnouncementFixture();
      f.business.actorRole = 20;
      try {
        await f.mount(tester);
        await f.open(tester);
        expect(find.text(f.text), findsOneWidget);
        expect(
          ModalRoute.of(tester.element(f.returnToChat))!.isCurrent,
          isTrue,
        );
        expect(f.writes, isEmpty);
        await f.closeDetail(tester);
        await f.ready(tester);
        expect(
          f.business.sdk.calls.where((c) => c.method == 'setGroupInfo'),
          isEmpty,
        );
        expect(tester.takeException(), isNull);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'acknowledged announcement close survives a new real chat route while a republished version appears again',
    (tester) async {
      final f = GroupAnnouncementFixture();
      try {
        await f.mount(tester);
        await f.ready(tester);
        await tester.tap(f.dismiss.hitTestable());
        await f.business.base.until(
          tester,
          () => f.banner.evaluate().isEmpty && f.writes.length == 1,
        );
        await f.leaveChat(tester);
        await f.openChat(tester);
        await f.business.base.until(tester, () => f.storeReads >= 2);
        await f.business.base.actual(tester, f.store.drain);
        await f.business.base.frame(tester);
        expect(f.banner, findsNothing);
        ++f.version;
        await f.business.base.actual(tester, f.groupEvent);
        await f.ready(tester);
        expect(f.writes, hasLength(1));
        expect(
          f.business.sdk.calls.where(
            (c) => c.method == 'resetConversationGroupAtType',
          ),
          isEmpty,
        );
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'a preferences failure retains the visible announcement until one explicit retry is acknowledged',
    (tester) async {
      final f = GroupAnnouncementFixture();
      f.failWrite = true;
      try {
        await f.mount(tester);
        await f.ready(tester);
        await tester.tap(f.dismiss.hitTestable());
        await f.business.base.ready(tester, find.text('关闭提醒未确认，请重试'));
        expect(f.banner, findsOneWidget);
        expect(f.values, isEmpty);
        f.failWrite = false;
        await tester.tap(f.dismiss.hitTestable());
        await f.business.base.until(tester, () => f.banner.evaluate().isEmpty);
        expect(f.values.values.single, isTrue);
        expect(f.writes, hasLength(2));
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'a delayed versionA dismissal never consumes a newly loaded versionB',
    (tester) async {
      final f = GroupAnnouncementFixture();
      try {
        await f.mount(tester);
        await f.ready(tester);
        f.writeReply = f.defer();
        f.writeEntered = Completer<void>();
        await tester.tap(f.dismiss.hitTestable());
        await f.business.base.until(tester, () => f.writeEntered!.isCompleted);
        f.text = '新的第二版公告';
        ++f.version;
        await f.business.base.actual(tester, f.groupEvent);
        expect(f.banner, findsNothing);
        f.writeReply!.complete();
        await f.ready(tester);
        expect(find.text('群公告：新的第二版公告'), findsOneWidget);
        expect(f.values, hasLength(1));
        expect(f.writes, hasLength(1));
        expect(
          f.business.sdk.calls.where(
            (c) => c.method == 'resetConversationGroupAtType',
          ),
          isEmpty,
        );
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'a genuine at-me hint does not create an announcement from historical group text',
    (tester) async {
      final f = GroupAnnouncementFixture();
      f.hint = 1;
      try {
        await f.mount(tester);
        await f.business.base.until(tester, () => f.storeReads == 1);
        await f.business.base.actual(tester, f.store.drain);
        await f.business.base.frame(tester);
        expect(find.byType(GroupAnnouncementPanel), findsOneWidget);
        expect(f.banner, findsNothing);
        expect(f.writes, isEmpty);
        expect(
          f.business.sdk.calls.where(
            (c) => c.method == 'resetConversationGroupAtType',
          ),
          isEmpty,
        );
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'failed fresh group metadata offers an actual read-only retry instead of an empty successful announcement',
    (tester) async {
      final f = GroupAnnouncementFixture();
      f.failRead = true;
      try {
        await f.mount(tester);
        final retry = find.widgetWithText(TextButton, '重新读取群公告');
        await f.business.base.ready(tester, retry);
        expect(f.banner, findsNothing);
        expect(f.writes, isEmpty);
        f.failRead = false;
        await tester.tap(retry.hitTestable());
        await f.ready(tester);
        expect(f.groupReads, greaterThanOrEqualTo(2));
        expect(f.writes, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );

  for (final invalid in ['duplicate', 'foreign', 'left']) {
    testWidgets(
      'fresh $invalid group metadata withdraws the announcement before any acknowledgement',
      (tester) async {
        final f = GroupAnnouncementFixture();
        try {
          await f.mount(tester);
          await f.ready(tester);
          final old = tester.widget<IconButton>(f.dismiss).onPressed!;
          if (invalid == 'left') {
            f.business.joined = false;
          } else {
            f.groupRows = [
              {
                'groupID': invalid == 'foreign' ? 'group-b' : 'group-a',
                'notification': '不应显示',
              },
              if (invalid == 'duplicate')
                {'groupID': 'group-a', 'notification': '冲突公告'},
            ];
          }
          await f.business.base.actual(tester, f.groupEvent);
          old();
          await f.business.base.ready(
            tester,
            find.widgetWithText(TextButton, '重新读取群公告'),
          );
          expect(f.banner, findsNothing);
          expect(find.text('不应显示'), findsNothing);
          expect(f.writes, isEmpty);
        } finally {
          await f.close(tester);
        }
      },
    );
  }

  for (final dimension in ['owner', 'issuer', 'tenant']) {
    testWidgets(
      'original $dimension replacement withdraws an open announcement and rejects its retained close callback',
      (tester) async {
        final f = GroupAnnouncementFixture();
        try {
          await f.mount(tester);
          await f.open(tester);
          final original = tester
              .widget<TextButton>(find.widgetWithText(TextButton, '关闭本条提醒'))
              .onPressed!;
          await f.business.base.actual(tester, () => f.changeOwner(dimension));
          await f.business.base.frame(tester);
          original();
          expect(find.text(f.text), findsNothing);
          expect(find.text('会话或群资料已变化，请返回聊天后重新读取公告'), findsOneWidget);
          expect(f.writes, isEmpty);
          await f.closeDetail(tester);
        } finally {
          await f.close(tester);
        }
      },
    );
  }

  testWidgets(
    'owner replacement while original group metadata waits prevents its next real member lookup',
    (tester) async {
      final f = GroupAnnouncementFixture();
      f.profileReply = f.defer();
      f.profileEntered = Completer<void>();
      try {
        await f.mount(tester);
        await f.business.base.until(
          tester,
          () => f.profileEntered!.isCompleted,
        );
        final generation = f.business.sdk.session.state.generation;
        late Future<void> changing;
        await tester.runAsync(() async {
          changing = f.changeOwner('owner');
        });
        expect(f.business.sdk.session.acceptsEvent(generation), isFalse);
        f.profileReply!.complete();
        await f.business.base.actual(tester, () => changing);
        await f.business.base.frame(tester);
        expect(
          f.business.sdk.calls.where((c) => c.method == 'getGroupMembersInfo'),
          isEmpty,
        );
        expect(f.writes, isEmpty);
        expect(f.banner, findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'a same-State CID ABA cannot renew the original announcement dialog or its acknowledgement',
    (tester) async {
      final f = GroupAnnouncementFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        final original = tester
            .widget<TextButton>(find.widgetWithText(TextButton, '关闭本条提醒'))
            .onPressed!;
        f.business.sourceCid.value = 'group-conversation-b';
        await f.business.base.frame(tester);
        f.business.sourceCid.value = 'group-conversation-a';
        await f.business.base.frame(tester);
        original();
        await f.business.base.frame(tester);
        expect(find.text(f.text), findsNothing);
        expect(f.writes, isEmpty);
        expect(find.text('会话或群资料已变化，请返回聊天后重新读取公告'), findsOneWidget);
        await f.closeDetail(tester);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'foreign coverage retires only the old owned announcement and a new explicit read remains usable',
    (tester) async {
      final f = GroupAnnouncementFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        final original = tester
            .widget<TextButton>(find.widgetWithText(TextButton, '关闭本条提醒'))
            .onPressed!;
        final foreign = await f.cover(tester);
        original();
        await f.business.base.frame(tester);
        expect(foreign.isCurrent, isTrue);
        expect(f.writes, isEmpty);
        await f.uncover(tester, foreign);
        expect(find.text(f.text), findsNothing);
        await f.closeDetail(tester);
        final retry = find.widgetWithText(TextButton, '重新读取群公告');
        await f.business.base.ready(tester, retry);
        await tester.tap(retry.hitTestable());
        await f.ready(tester);
        expect(f.banner, findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'foreground loss immediately rejects the old callback and the next frame withdraws announcement text',
    (tester) async {
      final f = GroupAnnouncementFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        final original = tester
            .widget<TextButton>(find.widgetWithText(TextButton, '关闭本条提醒'))
            .onPressed!;
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.inactive,
        );
        original();
        expect(f.writes, isEmpty);
        await f.business.base.frame(tester);
        expect(find.text(f.text), findsNothing);
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.resumed,
        );
        await f.business.base.frame(tester);
        original();
        expect(f.writes, isEmpty);
        await f.closeDetail(tester);
        final retry = find.widgetWithText(TextButton, '重新读取群公告');
        await f.business.base.ready(tester, retry);
        await tester.tap(retry.hitTestable());
        await f.ready(tester);
      } finally {
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.resumed,
        );
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'sameCID metadata refresh preserves composer draft and never consumes genuine strong hints',
    (tester) async {
      final f = GroupAnnouncementFixture();
      try {
        await f.mount(tester);
        await f.ready(tester);
        await tester.enterText(find.byType(TextField), '仍在编辑的群消息');
        f.business.metadata['showName'] = '更新后的群名称';
        f.business.metadata['unreadCount'] = 8;
        await f.business.base.actual(tester, f.groupEvent);
        await f.ready(tester);
        expect(
          tester.widget<TextField>(find.byType(TextField)).controller!.text,
          '仍在编辑的群消息',
        );
        expect(f.writes, isEmpty);
        expect(
          f.business.sdk.calls.where(
            (c) => c.method == 'resetConversationGroupAtType',
          ),
          isEmpty,
        );
        expect(
          f.business.sdk.calls.where((c) => c.method == 'sendMessage'),
          isEmpty,
        );
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'muted-admin announcement controls and full URL-like text remain usable at360 large text without automatic link behavior',
    (tester) async {
      final f = GroupAnnouncementFixture();
      f.business.actorMuted = true;
      f.text = 'www.example.com 为纯文本公告\n${'群活动说明。' * 80}';
      try {
        await f.mount(tester, small: true);
        await f.ready(tester);
        expect(tester.getSize(f.dismiss).width, greaterThanOrEqualTo(44));
        expect(tester.getSize(f.dismiss).height, greaterThanOrEqualTo(44));
        expect(find.byType(TextField).hitTestable(), findsOneWidget);
        await f.open(tester);
        expect(find.text(f.text), findsOneWidget);
        expect(tester.widget<Text>(find.text(f.text)).data, f.text);
        expect(tester.getSize(f.returnToChat).height, greaterThanOrEqualTo(44));
        await f.closeDetail(tester);
        expect(f.writes, isEmpty);
        expect(
          f.business.sdk.calls.where((c) => c.method == 'setGroupInfo'),
          isEmpty,
        );
        expect(tester.takeException(), isNull);
      } finally {
        await f.close(tester);
      }
    },
  );
}
