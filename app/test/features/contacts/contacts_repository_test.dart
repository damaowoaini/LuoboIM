import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/contacts/domain/contact.dart';
import 'package:imapp/features/group/data/group_repository.dart';

import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late TenantApi api;
  late ContactsRepository contacts;
  late GroupRepository groups;
  late TestHttpAdapter http;
  var actorRole = 100, targetRole = 20;
  Map<String, Object?> group() => {
    'groupID': 'group-a',
    'groupName': '测试群',
    'groupType': 2,
    'ownerUserID': 'alice',
    'memberCount': 4,
    'lookMemberInfo': 1,
    'applyMemberFriend': 1,
    'needVerification': 1,
  };
  setUp(() async {
    actorRole = 100;
    targetRole = 20;
    harness = SdkHarness();
    await harness.start();
    http = TestHttpAdapter(
      (_) async => jsonResponse(
        '{"errCode":0,"data":{"users":[{"userID":"bob","nickname":"Bob","allowAddFriend":0}],"total":1}}',
      ),
    );
    api = TenantApi(
      SessionAccess(harness.session),
      dio: Dio()..httpClientAdapter = http,
    );
    contacts = ContactsRepository(harness.gateway, api.access, api);
    groups = GroupRepository(harness.gateway, api.access);
    harness.overrideCall = (call) async {
      final args = call.arguments as Map;
      switch (call.method) {
        case 'getFriendList':
          return jsonEncode([
            {
              'friendInfo': {
                'userID': 'bob',
                'nickname': 'Bob',
                'remark': '甲方',
              },
            },
            {
              'friendInfo': {'userID': 'hidden', 'nickname': 'Blocked'},
              'blackInfo': {'userID': 'hidden'},
            },
            {
              'friendInfo': {'userID': 'carol', 'nickname': 'Carol'},
            },
          ]);
        case 'getUsersInfo':
          return jsonEncode([
            {
              'publicInfo': {'userID': 'bob', 'nickname': 'Bob'},
            },
          ]);
        case 'getGroupsInfo':
          return jsonEncode([group()]);
        case 'isJoinGroup':
          return 'true';
        case 'getGroupMembersInfo':
          return jsonEncode([
            for (final id in args['userIDList'] as List)
              {
                'groupID': 'group-a',
                'userID': id,
                'nickname': id,
                'roleLevel': id == 'alice' ? actorRole : targetRole,
              },
          ]);
        case 'createGroup':
          return jsonEncode(group());
        case 'getOneConversation':
          return jsonEncode({
            'conversationID': 'conversation-result',
            'unreadCount': 0,
            'conversationType': args['sessionType'],
          });
        case 'getFriendApplicationListAsRecipient':
          return jsonEncode([
            {
              'fromUserID': 'bob',
              'toUserID': 'alice',
              'fromNickname': 'Bob',
              'handleResult': 0,
              'createTime': 10,
            },
          ]);
        case 'getFriendApplicationListAsApplicant':
          return jsonEncode([
            {
              'fromUserID': 'alice',
              'toUserID': 'carol',
              'toNickname': 'Carol',
              'handleResult': 0,
              'createTime': 20,
            },
          ]);
      }
      return harness.respond(call);
    };
  });
  tearDown(() async {
    api.close();
    await harness.dispose();
  });

  test('friend remarks survive mapping and blocked friends stay out of the address book', () async {
    final values = await contacts.friends();
    expect(values.map((item) => item.id), unorderedEquals(['bob', 'carol']));
    expect(values.firstWhere((item) => item.id == 'bob').displayName, '甲方');
    await contacts.remark('bob', '新备注');
    expect(harness.calls.last.arguments['toUserID'], 'bob');
    expect(harness.calls.last.arguments['remark'], '新备注');
  });
  test(
    'search uses chat token, pagination and allowAddFriend=0 means denied',
    () async {
      final result = await contacts.searchUsers(' Bob ', page: 2, count: 10);
      expect(result.items.single.canRequestFriend, isFalse);
      final request = http.requests.single;
      expect(
        request.uri.toString(),
        'https://im.example.com/chat/user/search/full',
      );
      expect(request.headers['token'], 'fixture-chat');
      expect(request.data, {
        'keyword': 'Bob',
        'pagination': {'pageNumber': 2, 'showNumber': 10},
      });
      final profile = await contacts.profile('bob');
      expect(profile.canRequestFriend, isFalse);
    },
  );
  test('incoming and outgoing friend requests keep the other user and only incoming pending can be handled', () async {
    final values = await contacts.requests(ContactRequestKind.friend);
    expect(values.first.userId, 'carol');
    expect(values.first.incoming, isFalse);
    final before = harness.calls.length;
    await expectLater(
      contacts.handle(values.first, accept: true),
      throwsA(isA<AppFailure>()),
    );
    expect(harness.calls.length, before);
    await contacts.handle(values.last, accept: true);
    expect(harness.calls.last.method, 'acceptFriendApplication');
    expect(harness.calls.last.arguments['toUserID'], 'bob');
  });
  test('group flags keep prohibition semantics and booleans serialize to SDK status 1', () async {
    final value = await groups.profile('group-a');
    expect(value.hideMemberProfiles, isTrue);
    expect(value.preventMemberFriends, isTrue);
    await groups.setHideProfiles('group-a', true);
    expect(harness.calls.last.method, 'setGroupLookMemberInfo');
    expect(harness.calls.last.arguments['status'], 1);
    await groups.setPreventFriends('group-a', false);
    expect(harness.calls.last.arguments['status'], 0);
  });
  test('a withdrawn administrator role is re-read before a mutation', () async {
    expect((await groups.profile('group-a')).canManage, isTrue);
    actorRole = 20;
    await expectLater(
      groups.setMute('group-a', true),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.permissionDenied,
        ),
      ),
    );
    expect(
      harness.calls.any((call) => call.method == 'changeGroupMute'),
      isFalse,
    );
  });
  test(
    'administrators cannot moderate another administrator or themselves',
    () async {
      actorRole = 60;
      targetRole = 60;
      await expectLater(
        groups.muteMember('group-a', 'bob', 3600),
        throwsA(isA<AppFailure>()),
      );
      await expectLater(
        groups.remove('group-a', ['alice']),
        throwsA(isA<AppFailure>()),
      );
      await expectLater(
        groups.transfer('group-a', 'bob'),
        throwsA(isA<AppFailure>()),
      );
      expect(
        harness.calls.any(
          (call) => [
            'changeGroupMemberMute',
            'kickGroupMember',
            'transferGroupOwner',
          ].contains(call.method),
        ),
        isFalse,
      );
      targetRole = 20;
      await groups.muteMember('group-a', 'bob', 3600);
      expect(harness.calls.last.arguments['seconds'], 3600);
      expect(harness.calls.last.arguments['userID'], 'bob');
    },
  );
  test('creating a work group preserves unique other members and the group session type', () async {
    final conversation = await groups.create(' 新群 ', [
      'alice',
      'bob',
      'bob',
      'carol',
    ]);
    expect(conversation, 'conversation-result');
    final call = harness.calls.firstWhere(
      (call) => call.method == 'createGroup',
    );
    expect(call.arguments['memberUserIDs'], ['bob', 'carol']);
    expect(call.arguments['groupInfo']['groupType'], 2);
    expect(call.arguments['groupInfo']['groupName'], '新群');
    expect(harness.calls.last.arguments['sessionType'], 3);
  });
  test(
    'owner must transfer before leaving and cannot transfer to self',
    () async {
      await expectLater(groups.leave('group-a'), throwsA(isA<AppFailure>()));
      await expectLater(
        groups.transfer('group-a', 'alice'),
        throwsA(isA<AppFailure>()),
      );
      expect(harness.calls.any((call) => call.method == 'quitGroup'), isFalse);
    },
  );
  test(
    'logout during a membership read stops the following moderation request',
    () async {
      final response = Completer<Object?>();
      final started = Completer<void>();
      final original = harness.overrideCall!;
      harness.overrideCall = (call) async {
        if (call.method == 'getGroupMembersInfo' &&
            (call.arguments['userIDList'] as List).contains('bob')) {
          started.complete();
          return response.future;
        }
        return original(call);
      };
      final mutation = groups.muteMember('group-a', 'bob', 3600);
      final expected = expectLater(mutation, throwsA(isA<AppFailure>()));
      await started.future;
      final logout = harness.session.signOut();
      response.complete(
        jsonEncode([
          {'groupID': 'group-a', 'userID': 'bob', 'roleLevel': 20},
        ]),
      );
      await expected;
      await logout;
      expect(
        harness.calls.any((call) => call.method == 'changeGroupMemberMute'),
        isFalse,
      );
    },
  );
  test('old repositories cannot read or mutate the next account', () async {
    await harness.session.signOut();
    harness.userId = 'dave';
    await harness.session.signIn((_) async => harness.credentials);
    final before = harness.calls.length;
    await expectLater(contacts.friends(), throwsA(isA<AppFailure>()));
    await expectLater(contacts.block('bob', true), throwsA(isA<AppFailure>()));
    await expectLater(
      groups.invite('group-a', ['bob']),
      throwsA(isA<AppFailure>()),
    );
    await expectLater(api.im.post('fixture', {}), throwsA(isA<AppFailure>()));
    expect(harness.calls.length, before);
    expect(http.requests, isEmpty);
  });
  test('late HTTP results are cancelled on account invalidation, with separate chat and IM tokens', () async {
    await api.im.post('fixture', {});
    expect(http.requests.last.headers['token'], 'fixture-im');
    expect(http.requests.last.uri.path, '/api/fixture');
    final response = Completer<ResponseBody>();
    final started = Completer<void>();
    http = TestHttpAdapter((_) {
      started.complete();
      return response.future;
    });
    api.close();
    api = TenantApi(
      SessionAccess(harness.session),
      dio: Dio()..httpClientAdapter = http,
    );
    contacts = ContactsRepository(harness.gateway, api.access, api);
    final pending = contacts.searchUsers('private');
    final expected = expectLater(
      pending,
      throwsA(
        isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.cancelled),
      ),
    );
    await started.future;
    await harness.session.signOut();
    response.complete(
      jsonResponse('{"errCode":0,"data":{"users":[{"userID":"old-private"}]}}'),
    );
    await expected;
  });
}
