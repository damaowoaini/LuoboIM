import 'dart:convert';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/message_privacy.dart';
import 'package:imapp/features/favorite/domain/favorite.dart';

void main() {
  test('expiry starts at a known read timestamp and changes exactly at the duration boundary', () {
    const read = 1700000000123;
    final privacy = inspectMessagePrivacy({
      'isRead': true,
      'sendTime': 1600000000000,
      'hasReadTime': read,
      'attachedInfoElem': {'isPrivateChat': true, 'burnDuration': 30},
    });
    expect(privacy.expiresAtMilliseconds, read + 30000);
    expect(
      privacy.isExpiredAt(DateTime.fromMillisecondsSinceEpoch(read + 29999)),
      false,
    );
    expect(
      privacy.isExpiredAt(DateTime.fromMillisecondsSinceEpoch(read + 30000)),
      true,
    );
    expect(
      privacy.isExpiredAt(DateTime.fromMillisecondsSinceEpoch(read + 30001)),
      true,
    );
    final unread = inspectMessagePrivacy({
      'isRead': true,
      'sendTime': 1600000000000,
      'attachedInfoElem': {
        'isPrivateChat': true,
        'burnDuration': 30,
        'hasReadTime': 0,
      },
    });
    expect(unread.expiresAtMilliseconds, isNull);
  });
  test('legacy defaults, seconds compatibility bounds, malformed and overflow values are explicit', () {
    for (final duration in [null, 0, 30]) {
      expect(
        privateExpiryMilliseconds(
          readAt: 1700000000000,
          durationSeconds: duration,
        ),
        1700000030000,
      );
    }
    for (final read in [946684800, 4102444800]) {
      expect(
        privateExpiryMilliseconds(readAt: read, durationSeconds: 5),
        read * 1000 + 5000,
      );
    }
    for (final read in [946684800000, 4102444800000, 1700000000000]) {
      expect(
        privateExpiryMilliseconds(readAt: read, durationSeconds: 5),
        read + 5000,
      );
    }
    for (final read in [
      null,
      0,
      -1,
      1,
      123,
      946684799,
      4102444801,
      946684799999,
      4102444800001,
      8640000000000001,
    ]) {
      expect(
        privateExpiryMilliseconds(readAt: read, durationSeconds: 30),
        isNull,
      );
    }
    expect(
      privateExpiryMilliseconds(readAt: 1700000000000, durationSeconds: -1),
      isNull,
    );
    expect(
      privateExpiryMilliseconds(readAt: 8640000000000000, durationSeconds: 30),
      isNull,
    );
    expect(
      privateExpiryMilliseconds(
        readAt: 1700000000000,
        durationSeconds: 0x7fffffffffffffff,
      ),
      isNull,
    );
  });
  test('earliest actual private descendant expires its quote without inspecting opaque custom text', () {
    final privacy = inspectMessagePrivacy({
      'quoteElem': {
        'quoteMessage': {
          'hasReadTime': 1700000000000,
          'attachedInfoElem': {'isPrivateChat': true, 'burnDuration': 10},
        },
      },
    });
    expect(privacy.expiresAtMilliseconds, 1700000010000);
    expect(
      inspectMessagePrivacy({'keyVersion': 3, 'hasReadTime': 1700000000000})
          .expiresAtMilliseconds,
      isNull,
    );
  });
  test('mapper preserves exact private flag, both read timestamps and duration through copies without inventing a countdown', () {
    final item = mapMessage(
      Message.fromJson({
        'clientMsgID': 'private-message',
        'contentType': 101,
        'textElem': {'content': 'message'},
        'hasReadTime': 1700000000123,
        'attachedInfoElem': {
          'isPrivateChat': true,
          'hasReadTime': 1700000000456,
          'burnDuration': 0,
        },
      }),
      'alice',
    );
    for (final value in [
      item,
      item.withDelivery(item.delivery),
      item.withLocalMedia('/account/staged'),
    ]) {
      expect(value.privacy.isPrivateChat, true);
      expect(value.privacy.burnDurationSeconds, 0);
      expect(value.privacy.messageReadAtMilliseconds, 1700000000123);
      expect(value.privacy.attachmentReadAtMilliseconds, 1700000000456);
      expect(value.privacy.allowsRetention, false);
      expect(value.privacy.notice, contains('暂勿依赖本版本自动销毁'));
    }
  });

  test(
    'ordinary single chats and encrypted messages are not burn-after-reading',
    () {
      for (final flag in [null, false]) {
        final privacy = inspectMessagePrivacy({
          'sessionType': 1,
          'keyVersion': 27,
          'attachedInfoElem': {'isPrivateChat': flag},
        });
        expect(privacy.isPrivateChat, flag);
        expect(privacy.allowsRetention, true);
        expect(privacy.notice, isNull);
      }
    },
  );

  test('serialized metadata and private descendants preserve the retention boundary', () {
    final private = {
      'attachedInfo': jsonEncode({'isPrivateChat': true, 'burnDuration': 90}),
    };
    final root = inspectMessagePrivacy(private);
    expect(root.isPrivateChat, true);
    expect(root.burnDurationSeconds, 90);
    final quote = inspectMessagePrivacy({
      'quoteElem': {'quoteMessage': private},
    });
    final merge = inspectMessagePrivacy({
      'mergeElem': {
        'multiMessage': [private],
      },
    });
    for (final value in [quote, merge]) {
      expect(value.isPrivateChat, isNull);
      expect(value.containsPrivateContent, true);
      expect(value.allowsRetention, false);
    }
  });

  test('malformed or excessively nested privacy metadata cannot be treated as shareable', () {
    Object nested = {
      'attachedInfoElem': {'isPrivateChat': true},
    };
    for (var i = 0; i < 40; i++) {
      nested = {
        'quoteElem': {'quoteMessage': nested},
      };
    }
    for (final value in [
      {
        'attachedInfoElem': {'isPrivateChat': 'true'},
      },
      {'attachedInfo': '{broken'},
      nested,
    ]) {
      expect(inspectMessagePrivacy(value).inspectionComplete, false);
      expect(inspectMessagePrivacy(value).allowsRetention, false);
    }
  });

  test('opaque custom data is not interpreted as OpenIM privacy metadata', () {
    expect(
      inspectMessagePrivacy({
        'customElem': {'data': '{"isPrivateChat":true}'},
      }).allowsRetention,
      true,
    );
  });

  test('existing private favorite snapshots cannot expose forwarding while ordinary encrypted snapshots can', () {
    for (final private in [true, false]) {
      final content = FavoriteContent.decode(
        jsonEncode({
          'clientMsgID': 'old-source',
          'keyVersion': 5,
          'textElem': {'content': 'body'},
          'attachedInfoElem': {'isPrivateChat': private},
        }),
        FavoriteKind.message,
      );
      expect(content.canForward, !private);
      expect(content.privacy.containsPrivateContent, private);
    }
  });
  test('retentionBlocked is strict, defaults false and discards all content even if a response accidentally includes it', () {
    final base = <String, Object?>{
      'favoriteID': 'isolated',
      'userID': 'alice',
      'favoriteType': 3,
      'content': 'sensitive-body',
      'title': 'sensitive-title',
      'thumbnailURL': 'https://files.example.com/private',
      'notes': 'sensitive-notes',
      'tags': ['sensitive-tag'],
      'ex': 'sensitive-ex',
    };
    final old = FavoriteItem.fromJson(base, 'alice');
    expect(old.retentionBlocked, false);
    final blocked = FavoriteItem.fromJson({
      ...base,
      'retentionBlocked': true,
    }, 'alice');
    expect(blocked.displayTitle, '私密收藏已隔离');
    expect(blocked.content, isEmpty);
    expect(blocked.thumbnail, isEmpty);
    expect(blocked.notes, isEmpty);
    expect(blocked.tags, isEmpty);
    expect(blocked.ex, isEmpty);
    expect(blocked.decoded.media, isNull);
    expect(blocked.canUseContent, false);
    expect(blocked.decoded.canForward, false);
    for (final invalid in [0, 1, 'true', 'false']) {
      expect(
        () => FavoriteItem.fromJson({
          ...base,
          'retentionBlocked': invalid,
        }, 'alice'),
        throwsException,
      );
    }
  });

  test(
    'encoded JSON null keeps ordinary SDK previews and forwarding available',
    () {
      for (final encoded in ['null', ' \nnull\t']) {
        final wire = Message.fromJson({
          'clientMsgID': 'ordinary-null',
          'sendID': 'bob',
          'recvID': 'alice',
          'contentType': 101,
          'status': MessageStatus.succeeded,
          'attachedInfo': encoded,
          'textElem': {'content': 'synthetic ordinary text'},
        });
        expect(wire.toJson()['attachedInfo'], encoded);
        expect(messagePreview(wire), 'synthetic ordinary text');
        final item = mapMessage(wire, 'alice');
        expect(item.privacy.inspectionComplete, true);
        expect(item.privacy.containsPrivateContent, false);
        expect(
          item.privacy.canRetainAt(
            DateTime.fromMillisecondsSinceEpoch(1700000000000),
          ),
          true,
        );
        expect(item.canForward, true);
      }
    },
  );

  test('encoded null never masks modeled private metadata or real private descendants', () {
    final wire = Message.fromJson({
      'clientMsgID': 'private-null',
      'contentType': 101,
      'status': MessageStatus.succeeded,
      'attachedInfo': 'null',
      'textElem': {'content': 'synthetic private text'},
      'attachedInfoElem': {
        'isPrivateChat': true,
        'burnDuration': 30,
        'hasReadTime': 1700000000000,
      },
    });
    final item = mapMessage(wire, 'alice');
    expect(item.privacy.inspectionComplete, true);
    expect(item.privacy.containsPrivateContent, true);
    expect(item.privacy.expiresAtMilliseconds, 1700000030000);
    expect(item.canForward, false);
    expect(messagePreview(wire), '[阅后即焚消息]');
    final quote = inspectMessagePrivacy({
      'attachedInfo': 'null',
      'quoteElem': {'quoteMessage': wire.toJson()},
    });
    expect(quote.inspectionComplete, true);
    expect(quote.containsPrivateContent, true);
    expect(quote.expiresAtMilliseconds, 1700000030000);
    expect(quote.allowsRetention, false);
  });

  test('later encoded null cannot erase remembered private expiry or incomplete inspection', () async {
    var elapsed = 0;
    final wall = DateTime.fromMillisecondsSinceEpoch(1700000000000);
    final ledger = MessagePrivacyLedger(monotonicMilliseconds: () => elapsed)
      ..bind('issuer/tenant/user', 1);
    addTearDown(ledger.dispose);
    ledger.observe('c', {
      'clientMsgID': 'private-null',
      'attachedInfoElem': {
        'isPrivateChat': true,
        'hasReadTime': 1700000000000,
        'burnDuration': 30,
      },
    }, now: wall);
    final omitted = {'clientMsgID': 'private-null', 'attachedInfo': 'null'};
    final stillPrivate = ledger.observe('c', omitted, now: wall);
    expect(stillPrivate.containsPrivateContent, true);
    expect(stillPrivate.expiresAtMilliseconds, 1700000030000);
    expect(stillPrivate.canRetainAt(wall), false);
    elapsed = 30001;
    final rollback = wall.subtract(const Duration(days: 1));
    expect(stillPrivate.canViewAt(rollback), false);
    final expired = ledger.observe('c', omitted, now: rollback);
    expect(expired.canViewAt(rollback), false);
    expect(expired.allowsRetention, false);
    expect(
      ledger.observe('c', {
        'clientMsgID': 'malformed-null',
        'attachedInfo': '[]',
      }, now: rollback).inspectionComplete,
      false,
    );
    final incomplete = ledger.observe('c', {
      'clientMsgID': 'malformed-null',
      'attachedInfo': 'null',
    }, now: rollback);
    expect(incomplete.inspectionComplete, false);
    expect(incomplete.allowsRetention, false);
  });

  test('all other encoded nonobjects malformed fields and bounded tree violations remain rejected', () {
    for (final encoded in <Object?>[
      '[]',
      '[null]',
      'true',
      'false',
      '0',
      '1',
      '"null"',
      '"ordinary"',
      '{',
      'NULL',
      'null false',
      false,
      0,
      '{"isPrivateChat":"true"}',
      '{"burnDuration":1.5}',
      '{"hasReadTime":"1700000000000"}',
      'null'.padLeft(65537),
    ]) {
      final value = inspectMessagePrivacy({'attachedInfo': encoded});
      expect(value.inspectionComplete, false);
      expect(value.allowsRetention, false);
    }
    expect(
      inspectMessagePrivacy({'attachedInfo': 'null', 'attachedInfoElem': []})
          .inspectionComplete,
      false,
    );
    Object deep = {'attachedInfo': 'null'};
    for (var index = 0; index < 34; index++) {
      deep = {
        'quoteElem': {'quoteMessage': deep},
      };
    }
    expect(inspectMessagePrivacy(deep).inspectionComplete, false);
    final cycle = <String, Object?>{'attachedInfo': 'null'};
    cycle['quoteElem'] = {'quoteMessage': cycle};
    expect(inspectMessagePrivacy(cycle).inspectionComplete, false);
  });
}
