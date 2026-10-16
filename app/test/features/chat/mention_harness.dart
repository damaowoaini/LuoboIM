import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:dio/dio.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';

import '../../support/http_adapter.dart';

import '../../core/im/sdk_harness.dart';

class MentionHarness {
  final sdk = SdkHarness();
  int role = 20, status = 0;
  int muteEndTime = 0;
  bool joined = true, bobJoined = true;
  Future<dynamic> Function(MethodCall call)? intercept;
  Future<Object?> Function(RequestOptions request)? interceptHttp;
  TenantApi? _api;
  late final TestHttpAdapter http;
  TenantApi get api => _api ??= _makeApi();
  TenantApi _makeApi() {
    http = TestHttpAdapter(
      (request) async => jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': interceptHttp == null
              ? respondHttp(request)
              : await interceptHttp!(request),
        }),
      ),
    );
    return TenantApi(
      SessionAccess(sdk.session),
      dio: Dio()..httpClientAdapter = http,
    );
  }

  Object? respondHttp(RequestOptions request) {
    final data = request.data as Map;
    if (request.uri.path.endsWith('/group/get_groups_info')) {
      return {
        'groupInfos': [
          {'groupID': 'group-a', 'status': status, 'ownerUserID': 'alice'},
        ],
      };
    }
    if (request.uri.path.endsWith('/group/get_group_members_info')) {
      return {
        'members': members
            .where((m) => (data['userIDs'] as List).contains(m['userID']))
            .toList(),
      };
    }
    throw StateError('unexpected fixture IM path');
  }

  Future<void> dispose() async {
    _api?.close();
    await sdk.dispose();
  }

  MentionHarness() {
    sdk.conversations.add(SdkHarness.conversation(group: true, title: '成员讨论群'));
    sdk.messages.add({
      ...SdkHarness.message(id: 'source', text: '成员原消息'),
      'senderNickname': '同名成员',
      'groupID': 'group-a',
      'sessionType': 3,
    });
    sdk.overrideCall = (call) async =>
        intercept == null ? respond(call) : intercept!(call);
  }
  List<Map<String, Object?>> get members => [
    {
      'groupID': 'group-a',
      'userID': 'alice',
      'nickname': '自己',
      'roleLevel': role,
      'muteEndTime': muteEndTime,
    },
    if (bobJoined)
      {
        'groupID': 'group-a',
        'userID': 'bob',
        'nickname': '同名成员',
        'roleLevel': 20,
      },
    {
      'groupID': 'group-a',
      'userID': 'carol',
      'nickname': '同名成员',
      'roleLevel': 20,
    },
  ];
  dynamic respond(MethodCall call) {
    final args = call.arguments as Map;
    switch (call.method) {
      case 'getGroupsInfo':
        return jsonEncode([
          {
            'groupID': 'group-a',
            'groupName': '成员讨论群',
            'ownerUserID': 'alice',
            'memberCount': 3,
            'groupType': 2,
            'status': status,
          },
        ]);
      case 'isJoinGroup':
        return joined;
      case 'getGroupMembersInfo':
        return jsonEncode(
          members
              .where((m) => (args['userIDList'] as List).contains(m['userID']))
              .toList(),
        );
      case 'getGroupMemberList':
        return jsonEncode(
          members
              .skip(args['offset'] as int)
              .take(args['count'] as int)
              .toList(),
        );
      case 'searchGroupMembers':
        final search = args['searchParam'] as Map;
        final query = (search['keywordList'] as List).single as String;
        return jsonEncode(
          members
              .where((m) => m['userID'] == query || m['nickname'] == query)
              .skip(search['offset'] as int)
              .take(search['count'] as int)
              .toList(),
        );
      case 'createTextAtMessage':
        return jsonEncode({
          ...SdkHarness.message(
            id: 'created-at',
            text: '',
            sender: 'alice',
            status: 1,
          ),
          'contentType': 106,
          'groupID': 'group-a',
          'sessionType': 3,
          'atTextElem': {
            'text': args['text'],
            'atUserList': args['atUserIDList'],
            'atUsersInfo': args['atUserInfoList'],
            'quoteMessage': args['quoteMessage'],
          },
        });
      default:
        return sdk.respond(call);
    }
  }
}
