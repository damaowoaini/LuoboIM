import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/presentation/chat_location_picker_page.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';

import 'location_send_fixture.dart';

Finder sendButton() => find.widgetWithText(FilledButton, '发送此位置');
Finder gpsButton() => find.widgetWithText(OutlinedButton, '我的位置');

void main() {
  testWidgets(
    'actual attachment location entry with missing deployment config performs no map GPS or send',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester, configured: false);
        await f.open(tester);
        expect(find.text('地图服务配置不可用，暂不能选择位置'), findsOneWidget);
        expect(f.hosts, isEmpty);
        expect(f.native.requests, isEmpty);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'map readiness gates native GPS and opening the real location entry never locates automatically',
    (tester) async {
      final f = LocationSendFixture()..autoReady = false;
      try {
        await f.mount(tester);
        await f.open(tester);
        expect(tester.widget<OutlinedButton>(gpsButton()).onPressed, isNull);
        expect(f.native.requests, isEmpty);
        f.host.ready();
        await f.base.frame(tester);
        expect(tester.widget<OutlinedButton>(gpsButton()).onPressed, isNotNull);
        expect(tester.widget<FilledButton>(sendButton()).onPressed, isNull);
        expect(f.creates, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'actual attachment manual map selection sends one standard109 and returns only its own picker',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        expect(f.native.requests, isEmpty);
        await f.tap(tester, '发送此位置');
        await f.base.until(
          tester,
          () =>
              f.sends == 1 &&
              find.byType(ChatLocationPickerPage).evaluate().isEmpty,
        );
        expect(f.creates, 1);
        expect(find.byType(ChatPage), findsOneWidget);
        final sent = f.sdk.calls.singleWhere((v) => v.method == 'sendMessage');
        expect(sent.arguments['userID'], 'bob');
        expect(sent.arguments['message']['contentType'], 109);
        expect(
          sent.arguments['message']['locationElem']['description'],
          f.pick().description,
        );
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'provider configuration error keeps native actions disabled and explicit reload discards old document messages',
    (tester) async {
      final f = LocationSendFixture()..autoReady = false;
      try {
        await f.mount(tester);
        await f.open(tester);
        final old = f.host;
        old.failed();
        await f.base.frame(tester);
        expect(tester.widget<OutlinedButton>(gpsButton()).onPressed, isNull);
        expect(f.native.requests, isEmpty);
        expect(f.creates, 0);
        f.autoReady = true;
        await f.tap(tester, '重新加载地图');
        await f.base.until(
          tester,
          () => f.hosts.length == 2 && f.host.starts == 1,
        );
        old.pick(name: '旧文档位置');
        await f.base.frame(tester);
        expect(find.text('旧文档位置'), findsNothing);
        await f.select(tester, name: '新文档位置');
        expect(tester.widget<FilledButton>(sendButton()).onPressed, isNotNull);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'same CID metadata refresh keeps the manual selection and original valid history cut',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        f.sdk.conversations.single['showName'] = '新的联系人显示名';
        await f.base.actual(tester, f.controller.start);
        await f.base.frame(tester);
        expect(find.text('集合地点'), findsOneWidget);
        expect(tester.widget<FilledButton>(sendButton()).onPressed, isNotNull);
        await f.tap(tester, '发送此位置');
        await f.base.until(tester, () => f.sends == 1);
        expect(f.creates, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'explicit current position requests once converts WGS84 once and still waits for native confirmation',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.tap(tester, '我的位置');
        await f.base.until(tester, () => f.host.commands.isNotEmpty);
        expect(f.native.requests, hasLength(1));
        final conversion = f.host.commands.single;
        expect(conversion.$1, 'convertGps');
        expect(conversion.$2['requestNonce'], f.host.nonce);
        expect(conversion.$2['expectedSequence'], 0);
        expect(conversion.$2['lng'], 121.4);
        expect(conversion.$2['lat'], 31.1);
        expect(f.creates, 0);
        f.host.pick(name: '当前位置');
        await f.base.frame(tester);
        expect(tester.widget<FilledButton>(sendButton()).onPressed, isNotNull);
        expect(f.sends, 0);
        await f.tap(tester, '发送此位置');
        await f.base.until(tester, () => f.sends == 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'denied native GPS preserves manual map choice and remains explicitly retryable',
    (tester) async {
      final f = LocationSendFixture();
      f.native.reply = (_) async =>
          throw const AppFailure(FailureKind.permissionDenied);
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        await f.tap(tester, '我的位置');
        await f.base.until(
          tester,
          () => find.textContaining('未获得定位权限').evaluate().isNotEmpty,
        );
        expect(find.text('集合地点'), findsOneWidget);
        expect(tester.widget<FilledButton>(sendButton()).onPressed, isNotNull);
        expect(f.host.commands, isEmpty);
        f.native.reply = null;
        await f.tap(tester, '我的位置');
        await f.base.until(tester, () => f.host.commands.length == 1);
        expect(f.native.requests, hasLength(2));
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'manual reselection while one native fix is pending wins over its late response',
    (tester) async {
      final f = LocationSendFixture();
      final release = f.defer();
      f.native.reply = (id) async {
        await release.future;
        return f.native.fix(id);
      };
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        await f.tap(tester, '我的位置');
        await f.base.until(tester, () => f.native.requests.length == 1);
        await f.select(tester, sequence: 2, name: '后选位置');
        release.complete();
        await f.base.frame(tester);
        expect(f.host.commands, isEmpty);
        expect(find.text('后选位置'), findsOneWidget);
        expect(f.native.cancellations, contains(f.native.requests.single));
        expect(f.creates, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'owned system permission lifecycle can resume the same one-shot without granting a web GPS action',
    (tester) async {
      final f = LocationSendFixture();
      final release = f.defer();
      f.native.reply = (id) async {
        await release.future;
        return f.native.fix(id);
      };
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.tap(tester, '我的位置');
        await f.base.until(tester, () => f.native.requests.length == 1);
        for (final state in [
          AppLifecycleState.inactive,
          AppLifecycleState.resumed,
        ]) {
          tester.binding.handleAppLifecycleStateChanged(state);
          await tester.pump();
        }
        release.complete();
        await f.base.until(tester, () => f.host.commands.length == 1);
        expect(f.native.requests, hasLength(1));
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'foreign covering route and return permanently retire the old picker and its retained send callback',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        final old = f.host;
        final send = tester.widget<FilledButton>(sendButton()).onPressed!;
        await f.base.coverAndReturn(tester);
        old.pick(sequence: 2, name: '旧回调位置');
        send();
        await f.base.frame(tester);
        expect(find.text('旧回调位置'), findsNothing);
        expect(find.text('集合地点'), findsNothing);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'foreign coverage of the actual attachment popup cannot renew its location choice on return',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await tester.tap(find.byTooltip('发送附件').hitTestable());
        await f.base.until(
          tester,
          () => find.text('位置').evaluate().length == 1,
        );
        await tester.ensureVisible(find.text('位置'));
        await f.base.frame(tester);
        await f.base.ready(tester, find.text('位置'));
        await f.base.coverAndReturn(tester);
        await tester.tap(find.text('位置').hitTestable());
        await f.base.until(tester, () => find.text('位置').evaluate().isEmpty);
        await f.base.frame(tester);
        expect(find.byType(ChatLocationPickerPage), findsNothing);
        expect(f.hosts, isEmpty);
        expect(f.creates, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  for (final change in ['owner', 'issuer', 'tenant']) {
    testWidgets(
      'open picker withdraws candidate after original $change changes and ignores late page messages',
      (tester) async {
        final f = LocationSendFixture();
        try {
          await f.mount(tester);
          await f.open(tester);
          await f.select(tester);
          final old = f.host;
          await f.base.actual(
            tester,
            () => f.base.changeOwner(
              user: change == 'owner' ? 'carol' : 'alice',
              tenant: change == 'tenant'
                  ? 'another-tenant'
                  : f.sdk.credentials.tenantId,
              issuer: change == 'issuer'
                  ? 'https://another.example/'
                  : f.sdk.credentials.issuer,
            ),
          );
          old.pick(sequence: 2, name: '旧资料');
          await f.base.frame(tester);
          expect(find.text('集合地点'), findsNothing);
          expect(find.text('旧资料'), findsNothing);
          expect(f.native.requests, isEmpty);
          expect(f.creates, 0);
          expect(f.sends, 0);
        } finally {
          await f.close(tester);
        }
      },
    );
  }
  testWidgets(
    'clearing the original conversation retires map callbacks without acquiring a new cut',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        final old = f.host;
        await f.base.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            f.cid,
          ),
        );
        old.pick(sequence: 2, name: '清理后旧位置');
        await f.base.frame(tester);
        expect(find.text('集合地点'), findsNothing);
        expect(find.text('清理后旧位置'), findsNothing);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'late native creation after picker exit never proceeds to an SDK send',
    (tester) async {
      final f = LocationSendFixture();
      final entered = Completer<void>(), release = f.defer();
      f.beforeCreate = () async {
        entered.complete();
        await release.future;
      };
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        await f.tap(tester, '发送此位置');
        await f.base.until(tester, () => entered.isCompleted);
        final route = ModalRoute.of(
          tester.element(find.byType(ChatLocationPickerPage)),
        )!;
        f.base.navigator.currentState!.pop();
        var removed = false;
        unawaited(
          route.completed.then<void>((_) {
            removed = true;
          }),
        );
        await f.base.until(tester, () => removed);
        release.complete();
        await f.base.until(tester, () => !f.controller.preparing);
        expect(f.creates, 1);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'dispatched ACK after a foreign cover cannot pop that route or send twice',
    (tester) async {
      final f = LocationSendFixture();
      final entered = Completer<void>(), release = f.defer();
      f.beforeSend = () async {
        entered.complete();
        await release.future;
      };
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        final button = tester.widget<FilledButton>(sendButton()).onPressed!;
        button();
        button();
        await f.base.until(tester, () => entered.isCompleted);
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('保留外来页面')),
        );
        unawaited(f.base.navigator.currentState!.push(foreign));
        await f.base.ready(tester, find.text('保留外来页面'));
        release.complete();
        await f.base.until(tester, () => !f.controller.preparing);
        expect(find.text('保留外来页面').hitTestable(), findsOneWidget);
        expect(f.creates, 1);
        expect(f.sends, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'unknown location ACK is visible and the original retry paths do not replay the message',
    (tester) async {
      final f = LocationSendFixture()..failSend = true;
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        await f.tap(tester, '发送此位置');
        await f.base.until(
          tester,
          () => find.textContaining('发送结果未确认').evaluate().isNotEmpty,
        );
        expect(tester.widget<FilledButton>(sendButton()).onPressed, isNull);
        const id = 'location-created-1';
        await f.base.actual(tester, () => f.controller.resend(id));
        expect(f.controller.isUnconfirmedLocation(id), isTrue);
        expect(f.creates, 1);
        expect(f.sends, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'after unknown ACK a new explicitly opened picker can send a new selection while the original bubble cannot replay',
    (tester) async {
      final f = LocationSendFixture()..failSend = true;
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        await f.tap(tester, '发送此位置');
        await f.base.until(
          tester,
          () => find.textContaining('发送结果未确认').evaluate().isNotEmpty,
        );
        final first = f.host;
        final route = ModalRoute.of(
          tester.element(find.byType(ChatLocationPickerPage)),
        )!;
        f.base.navigator.currentState!.pop();
        var removed = false;
        unawaited(
          route.completed.then<void>((_) {
            removed = true;
          }),
        );
        await f.base.until(tester, () => removed);
        await f.base.frame(tester);
        expect(
          f.controller.isUnconfirmedLocation('location-created-1'),
          isTrue,
        );
        f.failSend = false;
        await f.open(tester);
        expect(f.host.nonce, isNot(first.nonce));
        expect(tester.widget<FilledButton>(sendButton()).onPressed, isNull);
        await f.select(tester, name: '新的独立位置');
        await f.tap(tester, '发送此位置');
        await f.base.until(
          tester,
          () =>
              f.sends == 2 &&
              find.byType(ChatLocationPickerPage).evaluate().isEmpty,
        );
        await f.base.actual(
          tester,
          () => f.controller.resend('location-created-1'),
        );
        expect(f.creates, 2);
        expect(f.sends, 2);
        expect(
          f.controller.isUnconfirmedLocation('location-created-1'),
          isTrue,
        );
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'same ChatPage State CID ABA retires its original picker and clears the old candidate',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        final old = f.host;
        final send = tester.widget<FilledButton>(sendButton()).onPressed!;
        f.sourceCid.value = 'other-cid';
        await f.base.frame(tester);
        f.sourceCid.value = f.cid;
        await f.base.frame(tester);
        old.pick(sequence: 2, name: '旧CID位置');
        send();
        await f.base.frame(tester);
        expect(find.text('集合地点'), findsNothing);
        expect(find.text('旧CID位置'), findsNothing);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'ordinary background without an owned GPS permission request retires the location intent',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        final send = tester.widget<FilledButton>(sendButton()).onPressed!;
        for (final state in [
          AppLifecycleState.inactive,
          AppLifecycleState.hidden,
          AppLifecycleState.paused,
          AppLifecycleState.hidden,
          AppLifecycleState.inactive,
          AppLifecycleState.resumed,
        ]) {
          tester.binding.handleAppLifecycleStateChanged(state);
          await tester.pump();
        }
        send();
        await f.base.frame(tester);
        expect(find.text('集合地点'), findsNothing);
        expect(f.native.requests, isEmpty);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'latest conversation send denial blocks a retained valid candidate before native creation',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.select(tester);
        final send = tester.widget<FilledButton>(sendButton()).onPressed!;
        f.sdk.conversations.single['isNotInGroup'] = true;
        await f.base.actual(tester, f.controller.start);
        await f.base.frame(tester);
        send();
        await f.base.frame(tester);
        expect(f.creates, 0);
        expect(f.sends, 0);
        expect(f.controller.conversation?.canSend, isFalse);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'map selection native controls and errors remain usable at360px with large text',
    (tester) async {
      final f = LocationSendFixture();
      try {
        await f.mount(tester, small: true);
        await f.open(tester);
        await f.select(tester, name: '需要核对的详细集合位置名称');
        await tester.ensureVisible(sendButton());
        await f.base.ready(tester, sendButton());
        expect(tester.getSize(sendButton()).height, greaterThanOrEqualTo(44));
        expect(
          MediaQuery.textScalerOf(tester.element(sendButton())).scale(10),
          16,
        );
        expect(tester.takeException(), isNull);
        await f.tap(tester, '发送此位置');
        await f.base.until(tester, () => f.sends == 1);
      } finally {
        await f.close(tester);
      }
    },
  );
}
