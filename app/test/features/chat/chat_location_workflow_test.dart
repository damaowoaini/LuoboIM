import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_location_view.dart';

import 'chat_location_fixture.dart';

void main() {
  testWidgets(
    'actual Location109 ChatPage card opens full local detail without fetching the legacy URL or locating',
    (tester) async {
      final f = ChatLocationFixture();
      var clients = 0;
      final semantics = tester.ensureSemantics();
      await HttpOverrides.runZoned(
        () async {
          try {
            await f.mount(tester);
            expect(find.text('人民公园'), findsOneWidget);
            expect(find.text('东门集合'), findsOneWidget);
            expect(
              find.bySemanticsLabel(RegExp(r'^查看位置详情(?:\n|$)')),
              findsOneWidget,
            );
            await f.open(tester);
            expect(find.text('纬度：31.2'), findsOneWidget);
            expect(find.text('经度：121.5'), findsOneWidget);
            expect(find.text('消息未标注坐标系'), findsOneWidget);
            expect(find.byType(Image), findsNothing);
            expect(
              f.sdk.calls.where(
                (c) =>
                    c.method == 'createLocationMessage' ||
                    c.method == 'sendMessage',
              ),
              isEmpty,
            );
          } finally {
            try {
              await f.close(tester);
            } finally {
              semantics.dispose();
            }
          }
        },
        createHttpClient: (_) {
          ++clients;
          throw StateError('Incoming location must not create an HTTP client');
        },
      );
      expect(clients, 0);
    },
  );

  testWidgets(
    'received explicitly supplied zero coordinates remain actionable',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(tester, message: f.wire(latitude: 0, longitude: 0));
        await f.open(tester);
        expect(find.text('纬度：0.0'), findsOneWidget);
        expect(find.text('经度：0.0'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'out of range received Location109 shows unavailable without an actionable location',
    (tester) async {
      final f = ChatLocationFixture();
      final semantics = tester.ensureSemantics();
      try {
        await f.mount(tester, message: f.wire(latitude: 91));
        expect(find.text('位置信息暂不可用'), findsOneWidget);
        expect(find.bySemanticsLabel(RegExp(r'^查看位置详情(?:\n|$)')), findsNothing);
        expect(find.byType(ChatLocationDetail), findsNothing);
        expect(tester.takeException(), isNull);
      } finally {
        try {
          await f.close(tester);
        } finally {
          semantics.dispose();
        }
      }
    },
  );
  testWidgets(
    'actual location card and scrollable full detail remain usable at 360px with large text',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(
          tester,
          small: true,
          message: f.wire(
            description: jsonEncode({
              'name': '人民公园南侧入口',
              'addr': '沿道路向东步行到入口，' * 30,
            }),
          ),
        );
        final size = tester.getSize(find.byType(ChatLocationView));
        expect(size.width, greaterThanOrEqualTo(44));
        expect(size.height, greaterThanOrEqualTo(44));
        await f.open(tester);
        await tester.ensureVisible(find.text('消息未标注坐标系'));
        await f.frame(tester);
        expect(find.text('消息未标注坐标系').hitTestable(), findsOneWidget);
        expect(
          MediaQuery.textScalerOf(tester.element(find.text('纬度：31.2')))
              .scale(10),
          16,
        );
        final back = find
            .widgetWithIcon(IconButton, Icons.arrow_back)
            .hitTestable();
        expect(back, findsOneWidget);
        expect(tester.getSize(back).height, greaterThanOrEqualTo(44));
        expect(tester.takeException(), isNull);
      } finally {
        await f.close(tester);
      }
    },
  );

  for (final event in ['revoke', 'expire', 'clear', 'clear-all']) {
    testWidgets(
      'an open location detail withdraws its coordinates after original $event without renewing its lease',
      (tester) async {
        final f = ChatLocationFixture();
        try {
          await f.mount(tester, message: f.wire(private: event == 'expire'));
          await f.open(tester);
          expect(find.text('纬度：31.2'), findsOneWidget);
          if (event == 'revoke') {
            await f.actual(
              tester,
              () => f.sdk.callback(ListenerType.advancedMsgListener, {
                'type': 'onNewRecvMessageRevoked',
                'data': {
                  'messageRevoked': jsonEncode({
                    'clientMsgID': 'received-location',
                  }),
                },
              }),
            );
          } else if (event == 'expire') {
            f.sdk.gateway.privacyLedger.observe(ChatLocationFixture.cid, {
              'clientMsgID': 'received-location',
              'attachedInfoElem': {
                'isPrivateChat': true,
                'hasReadTime': 1699999960000,
                'burnDuration': 30,
              },
            }, now: f.clock);
          } else if (event == 'clear') {
            await f.actual(
              tester,
              () => f.sdk.gateway.clearConversationMessages(
                f.sdk.session.state.generation,
                ChatLocationFixture.cid,
              ),
            );
          } else {
            await f.actual(
              tester,
              () => f.sdk.gateway.clearAllMessages(
                f.sdk.session.state.generation,
              ),
            );
          }
          await f.until(tester, () => find.text('纬度：31.2').evaluate().isEmpty);
          expect(find.text('人民公园'), findsNothing);
          expect(find.text('东门集合'), findsNothing);
          expect(find.text('消息内容已不可用'), findsOneWidget);
          expect(find.byType(ChatLocationDetail), findsOneWidget);
        } finally {
          await f.close(tester);
        }
      },
    );
  }

  for (final owner in ['account', 'tenant', 'issuer']) {
    testWidgets(
      'an open location detail hides the original owner coordinates on $owner replacement',
      (tester) async {
        final f = ChatLocationFixture();
        try {
          await f.mount(tester);
          await f.open(tester);
          final c = f.sdk.credentials;
          await f.actual(
            tester,
            () => f.changeOwner(
              user: owner == 'account' ? 'carol' : c.userId,
              tenant: owner == 'tenant' ? 'next-tenant' : c.tenantId,
              issuer: owner == 'issuer' ? 'https://next.example/' : c.issuer,
            ),
          );
          await f.until(tester, () => find.text('人民公园').evaluate().isEmpty);
          expect(find.text('纬度：31.2'), findsNothing);
          expect(find.text('消息内容已不可用'), findsOneWidget);
          expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
        } finally {
          await f.close(tester);
        }
      },
    );
  }
  testWidgets(
    'a retained location card callback cannot reopen after route ABA but a new explicit click can',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(tester);
        final old = f.cardCallback(tester);
        await f.coverAndReturn(tester);
        old();
        await f.frame(tester);
        expect(find.byType(ChatLocationDetail), findsNothing);
        await f.open(tester);
        expect(find.text('纬度：31.2'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'foreign cover retires an owned detail and its retained close cannot pop the foreign route',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        final close = tester
            .widget<IconButton>(
              find.widgetWithIcon(IconButton, Icons.arrow_back),
            )
            .onPressed!;
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('外来页面')),
        );
        unawaited(f.navigator.currentState!.push(foreign));
        await f.ready(tester, find.text('外来页面'));
        close();
        await f.frame(tester);
        expect(foreign.isCurrent, true);
        f.navigator.currentState!.pop();
        var completed = false;
        unawaited(
          foreign.completed.then<void>((_) {
            completed = true;
          }),
        );
        await f.until(tester, () => completed);
        expect(find.text('纬度：31.2'), findsNothing);
        expect(find.text('人民公园'), findsNothing);
        expect(find.text('消息内容已不可用'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'a retained preclear card callback cannot open a fresh history lease after clear',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(tester);
        final old = f.cardCallback(tester);
        await f.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            ChatLocationFixture.cid,
          ),
        );
        old();
        await f.frame(tester);
        expect(find.byType(ChatLocationDetail), findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'clearing another CID preserves the admitted original location detail',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            'single-alice-carol',
          ),
        );
        await f.frame(tester);
        expect(find.text('人民公园'), findsOneWidget);
        expect(find.text('纬度：31.2'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'same owner conversation metadata refresh preserves an admitted location detail',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        final detail = tester.widget<ChatLocationDetail>(
          find.byType(ChatLocationDetail),
        );
        final controller = detail.changes as ChatController;
        f.sdk.conversations.single['unreadCount'] = 8;
        await f.actual(tester, controller.refreshConversation);
        await f.frame(tester);
        expect(find.text('纬度：31.2'), findsOneWidget);
        expect(find.text('人民公园'), findsOneWidget);
        expect(detail.access.canView, true);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'viewable private location remains readable locally while retention and external actions stay unavailable',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(tester, message: f.wire(private: true));
        await f.open(tester);
        final detail = tester.widget<ChatLocationDetail>(
          find.byType(ChatLocationDetail),
        );
        expect(detail.access.canView, true);
        expect(detail.access.canRetain, false);
        expect(find.text('纬度：31.2'), findsOneWidget);
        expect(find.byType(SelectableText), findsNothing);
        expect(find.byType(Image), findsNothing);
        expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'a retained original account card callback cannot acquire the new account history lease',
    (tester) async {
      final f = ChatLocationFixture();
      try {
        await f.mount(tester);
        final old = f.cardCallback(tester);
        await f.actual(tester, () => f.changeOwner(user: 'carol'));
        old();
        await f.frame(tester);
        expect(find.byType(ChatLocationDetail), findsNothing);
        expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
  for (final event in [
    'revoke',
    'expire',
    'clear',
    'foreign-cover',
    'source-pop',
  ]) {
    testWidgets(
      'an actual merged location stays viewable in its owned detail until original $event retires it',
      (tester) async {
        final f = ChatLocationFixture();
        try {
          await f.mount(tester, merge: true);
          final origin = ModalRoute.of(
            tester.element(find.byType(ChatLocationView)),
          )!;
          await f.open(tester);
          final detail = tester.widget<ChatLocationDetail>(
            find.byType(ChatLocationDetail),
          );
          expect(detail.access.root.id, 'location-root');
          expect(detail.access.child.id, 'received-location');
          expect(detail.access.lease.conversationId, ChatLocationFixture.cid);
          expect(detail.access.canView, true);
          expect(find.text('纬度：31.2'), findsOneWidget);
          if (event == 'revoke') {
            await f.actual(
              tester,
              () => f.sdk.callback(ListenerType.advancedMsgListener, {
                'type': 'onNewRecvMessageRevoked',
                'data': {
                  'messageRevoked': jsonEncode({
                    'clientMsgID': 'location-root',
                  }),
                },
              }),
            );
          } else if (event == 'expire') {
            f.sdk.gateway.privacyLedger.observe(ChatLocationFixture.cid, {
              'clientMsgID': 'location-root',
              'attachedInfoElem': {
                'isPrivateChat': true,
                'hasReadTime': 1699999960000,
                'burnDuration': 30,
              },
            }, now: f.clock);
          } else if (event == 'clear') {
            await f.actual(
              tester,
              () => f.sdk.gateway.clearConversationMessages(
                f.sdk.session.state.generation,
                ChatLocationFixture.cid,
              ),
            );
          } else if (event == 'foreign-cover') {
            await f.coverAndReturn(tester);
          } else {
            f.navigator.currentState!.removeRoute(origin);
          }
          await f.until(tester, () => find.text('纬度：31.2').evaluate().isEmpty);
          expect(find.text('人民公园'), findsNothing);
          expect(find.text('消息内容已不可用'), findsOneWidget);
        } finally {
          await f.close(tester);
        }
      },
    );
  }
}
