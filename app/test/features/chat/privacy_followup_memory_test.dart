import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  var clock = DateTime.fromMillisecondsSinceEpoch(1700000000000);
  setUp(() async {
    clock = DateTime.fromMillisecondsSinceEpoch(1700000000000);
    harness = SdkHarness();
    harness.conversations.add(SdkHarness.conversation());
    await harness.start();
  });
  tearDown(() => harness.dispose());
  ChatRepository repository() => ChatRepository(
    gateway: harness.gateway,
    session: harness.session,
    conversationId: 'single-alice-bob',
    now: () => clock,
  );
  Map<String, dynamic> wire({int? read, bool flag = true}) => {
    ...SdkHarness.message(id: 'same-source', text: 'remembered-private-body'),
    if (flag)
      'attachedInfoElem': {
        'isPrivateChat': true,
        'burnDuration': 30,
        'hasReadTime': read,
      },
  };
  test('a remounted repository cannot turn observed private into retainable after metadata omission', () async {
    harness.messages.add(wire());
    final old = repository();
    final first = (await old.history()).messages.single;
    expect(first.privacy.allowsRetention, false);
    old.clear();
    harness.messages[0] = wire(flag: false);
    final fresh = (await repository().history()).messages.single;
    expect(fresh.privacy.allowsRetention, false);
  });
  test('expiry observed before rollback cannot resurrect after remount and metadata omission', () async {
    harness.messages.add(wire(read: 1699999960000));
    final old = repository();
    expect(
      (await old.history()).messages.single.privacy.isExpiredAt(clock),
      true,
    );
    old.clear();
    clock = DateTime.fromMillisecondsSinceEpoch(1600000000000);
    harness.messages[0] = wire(flag: false);
    expect(
      (await repository().history()).messages.single.privacy.isExpiredAt(clock),
      true,
    );
  });
  test('actual SDK conversation preview does not disclose private body or revive after its metadata disappears', () async {
    harness.conversations[0]['latestMsg'] = wire();
    final repo = ConversationRepository(harness.gateway, harness.session);
    expect(
      (await repo.list()).single.preview,
      isNot(contains('remembered-private-body')),
    );
    harness.conversations[0]['latestMsg'] = wire(flag: false);
    expect(
      (await ConversationRepository(
        harness.gateway,
        harness.session,
      ).list()).single.preview,
      isNot(contains('remembered-private-body')),
    );
  });
}
