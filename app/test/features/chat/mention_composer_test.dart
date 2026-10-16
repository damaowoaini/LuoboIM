import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/domain/chat_mention.dart';
import 'package:imapp/features/chat/presentation/mention_text_controller.dart';

void main() {
  MentionTextController create([String text = '']) {
    final controller = MentionTextController()..text = text;
    controller.selection = TextSelection.collapsed(offset: text.length);
    addTearDown(controller.dispose);
    return controller;
  }

  const bob = MentionIdentity('bob-id', '同名成员');
  test('an explicit insertion inside ordinary text gains a visible boundary with matching caret and wire range', () {
    final input = create('hello')..insert(bob);
    expect(input.text, 'hello @同名成员 ');
    expect(input.freeze().wireText, 'hello @bob-id ');
    expect(input.tokens.single.start, 6);
    expect(input.selection.baseOffset, input.text.length);
  });
  test('literal draft JSON never grants an unselected recipient when the editor reopens', () {
    const literal =
        '{"format":"luobo-mention-draft","version":1,"text":"@同名成员 ","tokens":[{"start":0,"end":5,"userID":"bob-id","name":"同名成员"}]}';
    final pasted = create(literal);
    expect(pasted.freeze().recipients, isEmpty);
    final reopened = create()..restore(pasted.storedDraft);
    expect(reopened.text, literal);
    expect(reopened.freeze().recipients, isEmpty);
  });
  test('historical stable UID containing at sign remains selectable and preserves exact wire identity', () {
    final input = create()
      ..insert(const MentionIdentity('legacy@example.test', '历史账号'));
    expect(input.freeze().recipients.single.userId, 'legacy@example.test');
    expect(input.freeze().wireText, '@legacy@example.test ');
    final restored = create()..restore(input.storedDraft);
    expect(restored.text, '@历史账号 ');
    expect(restored.freeze().recipients, isEmpty);
  });
  test('handwritten or pasted nickname and even UID text remain ordinary without a selected binding', () {
    final input = create('@同名成员 @bob-id ');
    expect(input.freeze().recipients, isEmpty);
    expect(input.freeze().wireText, input.text);
  });
  test('visible Unicode token and exact UTF16 range project to legacy UID only at frozen send time', () {
    final input = create('👩🏽‍💻你好 ');
    expect(input.insert(bob), true);
    final token = input.tokens.single;
    expect(input.text.substring(token.start, token.end), '@同名成员');
    expect(input.freeze().wireText, '👩🏽‍💻你好 @bob-id ');
    expect(input.selection.baseOffset, input.text.length);
    expect(input.text, contains('同名成员'));
  });
  test('editing inside a selected mention drops the ping rather than reinterpreting its new name', () {
    final input = create()..insert(bob);
    input.value = TextEditingValue(
      text: '@别的成员 ',
      selection: const TextSelection.collapsed(offset: 5),
    );
    expect(input.freeze().recipients, isEmpty);
  });
  test('edits before a mention shift its range in UTF16 without changing its stable recipient', () {
    final input = create('before ')..insert(bob);
    input.value = TextEditingValue(
      text: '😀${input.text}',
      selection: const TextSelection.collapsed(offset: 2),
    );
    expect(input.freeze().wireText, '😀before @bob-id ');
    expect(input.tokens.single.start, '😀before '.length);
  });
  test('extending the name into an ordinary word revokes its old binding', () {
    final input = create()..insert(bob);
    input.value = const TextEditingValue(
      text: '@同名成员X ',
      selection: TextSelection.collapsed(offset: 6),
    );
    expect(input.freeze().recipients, isEmpty);
  });
  test('selection replacement removes every overlapping mention and does not retain a ghost ID', () {
    final input = create()..insert(bob);
    input.selection = TextSelection(
      baseOffset: 0,
      extentOffset: input.text.length,
    );
    input.insert(const MentionIdentity('carol-id', '同名成员'));
    expect(input.freeze().recipients.map((v) => v.userId), ['carol-id']);
    expect(input.freeze().wireText, '@carol-id ');
  });
  test('IME composition overlapping a token removes binding and insertion never interrupts active composition', () {
    final input = create()..insert(bob);
    input.value = input.value.copyWith(
      composing: const TextRange(start: 1, end: 3),
    );
    expect(input.freeze().recipients, isEmpty);
    final before = input.value;
    expect(input.insert(bob), false);
    expect(input.value, before);
  });
  test('repeating one selected member creates multiple visible tokens but only one wire recipient', () {
    final input = create()
      ..insert(bob)
      ..insert(bob);
    expect(input.tokens, hasLength(2));
    expect(input.freeze().recipients, hasLength(1));
    expect(input.freeze().wireText, '@bob-id @bob-id ');
  });
  test('all is explicit and counts once alongside individual recipients up to the pinned10 limit', () {
    final input = create()..insert(const MentionIdentity(mentionAllId, '所有人'));
    for (var i = 0; i < 9; i++) {
      expect(input.insert(MentionIdentity('id-$i', '同名')), true);
    }
    expect(input.insert(bob), false);
    expect(input.freeze().recipients.map((m) => m.userId).first, mentionAllId);
    expect(input.freeze().recipients, hasLength(10));
  });
  test('reopening a plain draft preserves every character but requires new explicit recipient selection', () {
    final input = create('👋 ')..insert(bob);
    final restored = create()..restore(input.storedDraft);
    expect(input.freeze().wireText, '👋 @bob-id ');
    expect(restored.text, '👋 @同名成员 ');
    expect(restored.freeze().recipients, isEmpty);
    restored.restore('{"text":"@bob-id ","at":{"bob-id":"同名"}}');
    expect(restored.text, '{"text":"@bob-id ","at":{"bob-id":"同名"}}');
    expect(restored.tokens, isEmpty);
    restored.restore(
      '{"format":"luobo-mention-draft","version":1,"text":"hello","tokens":[{"start":0,"end":5,"userID":"bob-id","name":"wrong"}]}',
    );
    expect(restored.text, contains('"text":"hello"'));
    expect(restored.tokens, isEmpty);
  });
  test('explicit removal leaves visible text ordinary and new selections can be made after it', () {
    final input = create()..insert(bob);
    input.removeIdentity(bob.userId);
    expect(input.text, '@同名成员 ');
    expect(input.freeze().recipients, isEmpty);
    expect(input.insert(const MentionIdentity('carol', '同名成员')), true);
    expect(input.freeze().recipients.single.userId, 'carol');
  });
  testWidgets(
    'EditableText rendering and selection always address the same visible string',
    (tester) async {
      final input = create('👨‍👩‍👧 ')..insert(bob);
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: TextField(controller: input)),
        ),
      );
      final context = tester.element(find.byType(TextField));
      final span = input.buildTextSpan(context: context, withComposing: true);
      expect(span.toPlainText(), input.value.text);
      expect(input.selection.baseOffset, input.text.length);
      expect(span.toPlainText(), isNot(contains('bob-id')));
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
