import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/moments/data/moments_repository.dart';

import '../../core/privacy_bootstrap_test.dart'
    show StoreProbe, ImProbe, ConsentMemory;
import '../../support/http_adapter.dart';

class MomentHarness {
  final session = SessionCoordinator(store: StoreProbe(), im: ImProbe());
  late TenantApi api;
  late MomentsRepository repository;
  late TestHttpAdapter http;
  AppRuntime? _runtime;
  final items = <Map<String, Object?>>[item('one')];
  int unread = 2;
  Future<Object?> Function(RequestOptions)? overrideRequest;
  static Map<String, Object?> item(String id, {String author = 'alice'}) => {
    'workMomentID': id,
    'userID': author,
    'nickname': '晴空',
    'content': {'text': '今天的风很舒服，和朋友们分享一个新发现。', 'type': 0, 'metas': []},
    'permission': 2,
    'permissionUsers': [
      {'userID': 'bob', 'nickname': '小满'},
    ],
    'likeUsers': [
      {'userID': 'bob', 'nickname': '小满'},
    ],
    'atUsers': [],
    'comments': <Map<String, Object?>>[
      {
        'commentID': 'c1',
        'userID': 'bob',
        'nickname': '小满',
        'content': '记得一起去看看！',
        'createTime': '1792020000000',
      },
    ],
    'createTime': '1792010000000',
  };
  Future<void> start() async {
    await session.signIn(
      (_) async => SessionCredentials(
        userId: 'alice',
        tenantId: 'fixture',
        tenantName: '测试服务',
        domains: ['im.example.com'],
        chatToken: 'test-chat',
        imToken: 'test-im',
      ),
    );
    http = TestHttpAdapter(
      (request) async => jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': overrideRequest == null
              ? respond(request)
              : await overrideRequest!(request),
        }),
      ),
    );
    api = TenantApi(
      SessionAccess(session),
      dio: Dio()..httpClientAdapter = http,
    );
    repository = MomentsRepository(api);
  }

  AppRuntime get runtime => _runtime ??= AppRuntime(
    config: const AppConfig(),
    consent: ConsentMemory()..granted = true,
    routes: RouteConfigLoader(config: const AppConfig()),
    session: session,
    auth: (_, _) async => throw StateError('unused'),
  )..phase = AppPhase.online;
  Object? respond(RequestOptions request) {
    final operation = request.uri.path.replaceFirst(
      '/chat/office/work_moment/',
      '',
    );
    final body = Map<String, Object?>.from(request.data as Map);
    switch (operation) {
      case 'find/recv':
      case 'find/send':
      case 'logs':
        final paging = body['pagination'] as Map;
        final matches = operation == 'find/send'
            ? items.where((item) => item['userID'] == body['userID'])
            : items;
        return {
          'workMoments': matches
              .skip(
                ((paging['pageNumber'] as int) - 1) *
                    (paging['showNumber'] as int),
              )
              .take(paging['showNumber'] as int)
              .map((item) => {...item, if (operation == 'logs') 'type': 3})
              .toList(),
        };
      case 'get':
        return {
          'workMoment': items.firstWhere(
            (item) => item['workMomentID'] == body['workMomentID'],
          ),
        };
      case 'like':
        final target = items.firstWhere(
          (item) => item['workMomentID'] == body['workMomentID'],
        );
        final likes = target['likeUsers'] as List;
        likes.removeWhere((item) => item['userID'] == 'alice');
        if (body['like'] == true) {
          likes.add({'userID': 'alice', 'nickname': '晴空'});
        }
        return {'workMoment': target};
      case 'comment/add':
        final target = items.firstWhere(
          (item) => item['workMomentID'] == body['workMomentID'],
        );
        (target['comments'] as List).add({
          'commentID': 'new',
          'userID': 'alice',
          'nickname': '晴空',
          'replyUserID': body['replyUserID'],
          'content': body['content'],
        });
        return {'workMoment': target};
      case 'comment/del':
        final target = items.firstWhere(
          (item) => item['workMomentID'] == body['workMomentID'],
        );
        (target['comments'] as List).removeWhere(
          (item) => item['commentID'] == body['commentID'],
        );
        return {'workMoment': target};
      case 'del':
        items.removeWhere(
          (item) => item['workMomentID'] == body['workMomentID'],
        );
        return null;
      case 'add':
        final value = {
          ...item('created'),
          'content': body['content'],
          'permission': body['permission'],
        };
        items.insert(0, value);
        return {'workMoment': value};
      case 'unread/count':
        return {'total': unread};
      case 'unread/clear':
        unread = 0;
        return null;
    }
    throw StateError('unexpected fixture operation');
  }

  Future<void> dispose() async {
    api.close();
    _runtime?.dispose();
    await session.dispose();
  }
}
