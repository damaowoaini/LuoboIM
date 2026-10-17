import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/mention_text_controller.dart';

import 'chat_input_fixture.dart';

void main() {
  testWidgets(
    'self revoke ACK restores text once through the real composer without sending',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester, small: true);
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        await f.revoke.base.ready(tester, f.restore);
        expect(tester.getSize(f.restore).height, greaterThanOrEqualTo(44));
        final old = tester.widget<TextButton>(f.restore).onPressed!;
        await tester.tap(f.restore.hitTestable());
        final input = tester.widget<TextField>(f.editor).controller!;
        expect(input.text, '待核对的原消息');
        expect(input.selection.baseOffset, input.text.length);
        input.clear();
        old();
        expect(input.text, isEmpty);
        expect(
          f.revoke.sdk.calls.where((c) => c.method == 'sendMessage'),
          isEmpty,
        );
        expect(f.revoke.revokes, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'self at-text ACK restores exact mention identities instead of guessing from draft text',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester, group: true, at: true);
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        await f.revoke.base.ready(tester, f.restore);
        await tester.tap(f.restore.hitTestable());
        final input =
            tester.widget<TextField>(f.editor).controller!
                as MentionTextController;
        expect(input.text, 'Hello @鲍勃 ');
        expect(input.freeze().recipients.single.userId, 'bob');
        expect(input.freeze().wireText, 'Hello @bob ');
        expect(input.freeze().valid, isTrue);
        expect(
          f.revoke.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
          isEmpty,
        );
        expect(tester.takeException(), isNull);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'unknown revoke never offers re-edit even when the native side removed the original',
    (tester) async {
      final f = ChatInputFixture();
      f.revoke.nativeFailure = true;
      f.revoke.committedBeforeFailure = true;
      try {
        await f.mount(tester);
        await f.openRevoke(tester);
        await tester.tap(f.revoke.confirm.hitTestable());
        await f.revoke.base.ready(
          tester,
          find.widgetWithText(TextButton, '只读核对'),
        );
        expect(f.revoke.sdk.messages, isEmpty);
        expect(f.restore, findsNothing);
        await f.revoke.closeDialog(tester);
        expect(f.restore, findsNothing);
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
        expect(f.revoke.revokes, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'an acknowledged moderated peer text never becomes the local sender draft',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester, group: true, self: false);
        await f.openRevoke(tester, self: false);
        await f.acknowledge(tester);
        expect(f.revoke.revokes, 1);
        expect(f.restore, findsNothing);
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'private self text can be revoked but cannot be retained as a re-edit draft',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester, private: true);
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        expect(f.revoke.revokes, 1);
        expect(f.restore, findsNothing);
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'incomplete attached privacy metadata cannot be retained by successful self revoke',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester, malformed: true);
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        expect(f.revoke.revokes, 1);
        expect(f.restore, findsNothing);
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'explicit re-edit preserves an existing draft until the user clears it',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester, draft: '原有草稿');
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        await f.revoke.base.ready(tester, f.restore);
        await tester.tap(f.restore.hitTestable());
        expect(tester.widget<TextField>(f.editor).controller!.text, '原有草稿');
        await tester.enterText(f.editor, '');
        await tester.tap(f.restore.hitTestable());
        expect(tester.widget<TextField>(f.editor).controller!.text, '待核对的原消息');
        expect(
          f.revoke.sdk.calls.where((c) => c.method == 'sendMessage'),
          isEmpty,
        );
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'foreign route coverage permanently retires the old re-edit callback after return',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        await f.revoke.base.ready(tester, f.restore);
        final old = tester.widget<TextButton>(f.restore).onPressed!;
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('独立资料')),
        );
        unawaited(f.revoke.base.navigator.currentState!.push(foreign));
        await f.revoke.base.ready(tester, find.text('独立资料'));
        old();
        f.revoke.base.navigator.currentState!.pop();
        await f.revoke.base.actual(tester, () => foreign.completed);
        old();
        expect(f.restore, findsNothing);
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'history clear after ACK immediately blocks a retained re-edit action',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        await f.revoke.base.ready(tester, f.restore);
        final old = tester.widget<TextButton>(f.restore).onPressed!;
        await f.revoke.base.actual(
          tester,
          () => f.revoke.sdk.gateway.clearConversationMessages(
            f.revoke.sdk.session.state.generation,
            f.revoke.cid,
          ),
        );
        old();
        await tester.pump();
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
        expect(f.restore, findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'new sticky private facts after ACK prevent the old re-edit callback from copying text',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        await f.revoke.base.ready(tester, f.restore);
        final old = tester.widget<TextButton>(f.restore).onPressed!;
        f.revoke.sdk.gateway.privacyLedger.observe(f.revoke.cid, {
          'clientMsgID': 'moderated-source',
          'attachedInfoElem': {'isPrivateChat': true},
        }, now: f.revoke.base.clock);
        old();
        await tester.pump();
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
        expect(f.restore, findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'an explicit delete notice after self revoke cancels its retained re-edit action',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await f.openRevoke(tester);
        await f.acknowledge(tester);
        await f.revoke.base.ready(tester, f.restore);
        final old = tester.widget<TextButton>(f.restore).onPressed!;
        await f.deleteNotice(tester);
        old();
        await tester.pump();
        expect(f.restore, findsNothing);
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'same-State CID ABA during a dispatched revoke cannot restore its late acknowledged text',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await f.openRevoke(tester);
        final entered = Completer<void>(), release = f.revoke.defer();
        f.revoke.beforeNative = () async {
          entered.complete();
          await release.future;
        };
        await tester.tap(f.revoke.confirm.hitTestable());
        await f.revoke.base.until(tester, () => entered.isCompleted);
        f.revoke.sourceCid.value = 'other-cid';
        await tester.pump();
        f.revoke.sourceCid.value = f.revoke.cid;
        await tester.pump();
        release.complete();
        await f.revoke.base.until(tester, () => f.revoke.sdk.messages.isEmpty);
        await f.revoke.base.frame(tester);
        expect(f.revoke.revokes, 1);
        expect(f.restore, findsNothing);
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'owner logout while revoke waits keeps the ACK from writing the retired composer',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await f.openRevoke(tester);
        final entered = Completer<void>(), release = f.revoke.defer();
        f.revoke.beforeNative = () async {
          entered.complete();
          await release.future;
        };
        await tester.tap(f.revoke.confirm.hitTestable());
        await f.revoke.base.until(tester, () => entered.isCompleted);
        final logout = f.revoke.sdk.session.signOut();
        await tester.pump();
        release.complete();
        await f.revoke.base.actual(tester, () => logout);
        expect(f.restore, findsNothing);
        expect(f.revoke.revokes, 1);
        expect(tester.widget<TextField>(f.editor).controller!.text, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );
}
