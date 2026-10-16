import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/privacy/message_privacy.dart';

void main() {
  final wall = DateTime.fromMillisecondsSinceEpoch(1700000000000);
  Map<String, Object?> message(
    String id, {
    bool private = false,
    int? read,
    int duration = 30,
  }) => {
    'clientMsgID': id,
    'textElem': {'content': 'synthetic-body-not-retained'},
    'attachedInfoElem': {
      'isPrivateChat': private,
      'hasReadTime': read,
      'burnDuration': duration,
    },
  };
  test('elapsed monotonic time advances a private deadline despite wall rollback and remount', () async {
    var elapsed = 0;
    final ledger = MessagePrivacyLedger(monotonicMilliseconds: () => elapsed)
      ..bind('issuer-A/owner', 1);
    addTearDown(ledger.dispose);
    final old = ledger.observe(
      'c',
      message('m', private: true, read: 1700000000000),
      now: wall,
    );
    expect(old.canViewAt(wall), true);
    elapsed = 30000;
    final rollback = wall.subtract(const Duration(days: 365));
    expect(old.canViewAt(rollback), false);
    final fresh = ledger.observe('c', {'clientMsgID': 'm'}, now: rollback);
    expect(fresh.canViewAt(rollback), false);
    expect(fresh.allowsRetention, false);
  });
  test(
    'private metadata and earliest deadline cannot be erased or extended',
    () async {
      final ledger = MessagePrivacyLedger()..bind('A', 1);
      addTearDown(ledger.dispose);
      ledger.observe(
        'c',
        message('m', private: true, read: 1700000000000),
        now: wall,
      );
      final extended = ledger.observe(
        'c',
        message('m', private: true, read: 1700000090000, duration: 300),
        now: wall,
      );
      expect(extended.expiresAtMilliseconds, 1700000030000);
      final omitted = ledger.observe('c', {'clientMsgID': 'm'}, now: wall);
      expect(omitted.containsPrivateContent, true);
      expect(omitted.allowsRetention, false);
    },
  );
  test('actual descendants consult remembered identity and an old quote reacts to later private metadata', () async {
    final ledger = MessagePrivacyLedger()..bind('A', 1);
    addTearDown(ledger.dispose);
    final quote = ledger.observe('c', {
      'clientMsgID': 'q',
      'atTextElem': {'quoteMessage': message('child')},
    }, now: wall);
    expect(quote.allowsRetention, true);
    ledger.observe('c', message('child', private: true), now: wall);
    expect(quote.allowsRetention, false);
    final remount = ledger.observe('c', {
      'clientMsgID': 'q',
      'atTextElem': {
        'quoteMessage': {'clientMsgID': 'child'},
      },
    }, now: wall);
    expect(remount.containsPrivateContent, true);
  });
  test(
    'deletion and history tombstones survive later metadata-only refill',
    () async {
      final ledger = MessagePrivacyLedger()..bind('A', 1);
      addTearDown(ledger.dispose);
      ledger.observe('c', message('m'), now: wall);
      ledger.hideKnown(conversationId: 'c');
      final stale = ledger.observe('c', message('m'), now: wall);
      expect(stale.canViewAt(wall), false);
      expect(stale.allowsRetention, false);
      ledger.forget(['unknown-deleted']);
      expect(
        ledger
            .observe('c', message('unknown-deleted'), now: wall)
            .canViewAt(wall),
        false,
      );
    },
  );
  test('new owner or actual session is independent, and old projections never consult the new owner', () async {
    final ledger = MessagePrivacyLedger()..bind('issuer-A/user', 1);
    addTearDown(ledger.dispose);
    final old = ledger.observe('c', message('same', private: true), now: wall);
    ledger.bind('issuer-B/user', 2);
    final fresh = ledger.observe('c', message('same'), now: wall);
    expect(fresh.canRetainAt(wall), true);
    expect(old.canViewAt(wall), false);
    ledger.bind('issuer-B/user', 3);
    expect(fresh.canViewAt(wall), false);
  });
  test('unknown read time leaves private inline media viewable but denies every retention operation', () async {
    final ledger = MessagePrivacyLedger()..bind('A', 1);
    addTearDown(ledger.dispose);
    final value = ledger.observe('c', message('m', private: true), now: wall);
    expect(value.expiresAtMilliseconds, isNull);
    expect(value.canViewAt(wall), true);
    expect(value.canRetainAt(wall), false);
  });
  test('bounded metadata capacity fails closed without discarding existing tombstones', () async {
    final ledger = MessagePrivacyLedger(maximumEntries: 2)..bind('A', 1);
    addTearDown(ledger.dispose);
    ledger.observe('c', message('one'), now: wall);
    ledger.forget(['one']);
    ledger.observe('c', message('two'), now: wall);
    final overflow = ledger.observe(
      'c',
      message('three', private: true),
      now: wall,
    );
    expect(overflow.canViewAt(wall), false);
    expect(overflow.allowsRetention, false);
    expect(ledger.lookup('one').canViewAt(wall), false);
  });
  test('a favorite reference is ordinary while capacity is available but cannot bypass an unrecordable private source at capacity', () async {
    final ledger = MessagePrivacyLedger(maximumEntries: 1)..bind('A', 1);
    addTearDown(ledger.dispose);
    final unknown = ledger.retentionReference('omitted-new-private');
    expect(unknown.allowsRetention, true);
    final ordinary = ledger.observe('c', message('known-ordinary'), now: wall);
    expect(unknown.allowsRetention, false);
    expect(unknown.containsPrivateContent, false);
    final overflow = ledger.observe(
      'c',
      message('omitted-new-private', private: true),
      now: wall,
    );
    expect(overflow.allowsRetention, false);
    expect(
      ledger.retentionReference('omitted-new-private').allowsRetention,
      false,
    );
    expect(ordinary.canViewAt(wall), true);
    expect(ordinary.allowsRetention, true);
  });
  test('shared DAG projection is memoized and later leaf privacy still reaches old ancestors', () async {
    final ledger = MessagePrivacyLedger()..bind('A', 1);
    addTearDown(ledger.dispose);
    ledger.observe('c', message('n0'), now: wall);
    ledger.observe('c', message('n1'), now: wall);
    MessagePrivacy? root;
    for (var i = 2; i < 29; i++) {
      root = ledger.observe('c', {
        'clientMsgID': 'n$i',
        'mergeElem': {
          'multiMessage': [
            {'clientMsgID': 'n${i - 1}'},
            {'clientMsgID': 'n${i - 2}'},
          ],
        },
      }, now: wall);
    }
    expect(root!.allowsRetention, true);
    ledger.observe('c', message('n0', private: true), now: wall);
    expect(root.containsPrivateContent, true);
    expect(root.allowsRetention, false);
  });
  test(
    'a cross-observation cycle or over-deep remembered graph fails closed',
    () async {
      final ledger = MessagePrivacyLedger()..bind('A', 1);
      addTearDown(ledger.dispose);
      final cyclic = ledger.observe('c', {
        'clientMsgID': 'a',
        'quoteElem': {
          'quoteMessage': {'clientMsgID': 'b'},
        },
      }, now: wall);
      ledger.observe('c', {
        'clientMsgID': 'b',
        'quoteElem': {
          'quoteMessage': {'clientMsgID': 'a'},
        },
      }, now: wall);
      expect(cyclic.inspectionComplete, false);
      expect(cyclic.canViewAt(wall), false);
      ledger.observe('c', message('deep0', private: true), now: wall);
      MessagePrivacy? deep;
      for (var i = 1; i < 40; i++) {
        deep = ledger.observe('c', {
          'clientMsgID': 'deep$i',
          'quoteElem': {
            'quoteMessage': {'clientMsgID': 'deep${i - 1}'},
          },
        }, now: wall);
      }
      expect(deep!.inspectionComplete, false);
      expect(deep.canRetainAt(wall), false);
      expect(deep.canViewAt(wall), false);
    },
  );
}
