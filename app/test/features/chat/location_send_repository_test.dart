import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';
import 'package:imapp/features/chat/domain/location_pick.dart';

import 'location_send_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  for (final group in [false, true]) {
    test(
      'real SDK Location109 ${group ? 'group' : 'single'} sends original recipient and exact GCJ02 wire',
      () async {
        final f = LocationSendFixture();
        await f.startRepository(group: group);
        try {
          final access = f.sdk.gateway.historyAccess(
            f.sdk.session.state.generation,
            conversationId: f.cid,
          );
          final pick = f.pick();
          final result = await f.controller.sendLocation(
            pick,
            access: access,
            checkSource: () {},
          );
          expect(result.kind, LocationSendKind.sent);
          expect(f.creates, 1);
          expect(f.sends, 1);
          final call = f.sdk.calls.singleWhere(
            (v) => v.method == 'sendMessage',
          );
          expect(
            call.arguments[group ? 'groupID' : 'userID'],
            group ? 'group-a' : 'bob',
          );
          expect(call.arguments['message']['contentType'], 109);
          expect(call.arguments['message']['locationElem'], {
            'latitude': pick.latitude,
            'longitude': pick.longitude,
            'description': pick.description,
          });
          expect(
            f.controller.messages
                .singleWhere((v) => v.id == result.messageId)
                .kind,
            ChatMessageKind.location,
          );
        } finally {
          await f.closeRepository();
        }
      },
    );
  }
  test('invalid native Location message is rejected before any send', () async {
    final f = LocationSendFixture();
    await f.startRepository();
    try {
      f.invalidCreate = true;
      final result = await f.controller.sendLocation(
        f.pick(),
        access: f.sdk.gateway.historyAccess(
          f.sdk.session.state.generation,
          conversationId: f.cid,
        ),
        checkSource: () {},
      );
      expect(result.kind, LocationSendKind.notDispatched);
      expect(f.creates, 1);
      expect(f.sends, 0);
    } finally {
      await f.closeRepository();
    }
  });
  test('a frozen history cut invalidated before preparation cannot borrow a new lease', () async {
    final f = LocationSendFixture();
    await f.startRepository();
    try {
      final cut = f.sdk.gateway.historyAccess(
        f.sdk.session.state.generation,
        conversationId: f.cid,
      );
      await f.sdk.gateway.clearConversationMessages(
        f.sdk.session.state.generation,
        f.cid,
      );
      final result = await f.controller.sendLocation(
        f.pick(),
        access: cut,
        checkSource: () {},
      );
      expect(result.kind, LocationSendKind.notDispatched);
      expect(f.creates, 0);
      expect(f.sends, 0);
    } finally {
      await f.closeRepository();
    }
  });
  test('source retirement during actual native creation prevents subsequent SDK send', () async {
    final f = LocationSendFixture();
    await f.startRepository();
    try {
      final entered = Completer<void>(), release = f.defer();
      f.beforeCreate = () async {
        entered.complete();
        await release.future;
      };
      var current = true;
      final pending = f.controller.sendLocation(
        f.pick(),
        access: f.sdk.gateway.historyAccess(
          f.sdk.session.state.generation,
          conversationId: f.cid,
        ),
        checkSource: () {
          if (!current) {
            throw const AppFailure(FailureKind.cancelled);
          }
        },
      );
      await entered.future;
      expect(f.creates, 1);
      current = false;
      release.complete();
      expect((await pending).kind, LocationSendKind.notDispatched);
      expect(f.sends, 0);
    } finally {
      await f.closeRepository();
    }
  });
  test('double admission dispatches once and unknown ACK cannot create or resend a second message', () async {
    final f = LocationSendFixture();
    await f.startRepository();
    try {
      final entered = Completer<void>(), release = f.defer();
      f.beforeSend = () async {
        entered.complete();
        await release.future;
      };
      f.failSend = true;
      final access = f.sdk.gateway.historyAccess(
        f.sdk.session.state.generation,
        conversationId: f.cid,
      );
      final first = f.controller.sendLocation(
        f.pick(),
        access: access,
        checkSource: () {},
      );
      await entered.future;
      expect(
        (await f.controller.sendLocation(
          f.pick(2),
          access: access,
          checkSource: () {},
        )).kind,
        LocationSendKind.notDispatched,
      );
      release.complete();
      final result = await first;
      expect(result.kind, LocationSendKind.unconfirmed);
      await f.controller.resend(result.messageId!);
      expect(
        (await f.controller.sendLocation(
          f.pick(3),
          access: access,
          checkSource: () {},
        )).kind,
        LocationSendKind.notDispatched,
      );
      expect(f.creates, 1);
      expect(f.sends, 1);
      expect(f.controller.isUnconfirmedLocation(result.messageId!), isTrue);
    } finally {
      await f.closeRepository();
    }
  });
}
