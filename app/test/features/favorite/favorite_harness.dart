import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/favorite/data/favorite_repository.dart';

import '../../core/privacy_bootstrap_test.dart'
    show StoreProbe, ImProbe, ConsentMemory;
import '../../support/http_adapter.dart';

class FavoriteHarness {
  final session = SessionCoordinator(store: StoreProbe(), im: ImProbe());
  late TenantApi api;
  late FavoriteRepository repository;
  late TestHttpAdapter http;
  AppRuntime? _runtime;
  Future<Object?> Function(RequestOptions)? overrideRequest;
  final items = <Map<String, Object?>>[];
  final categories = <Map<String, Object?>>[
    {
      'categoryID': 'default',
      'userID': 'alice',
      'categoryName': '默认分类',
      'categoryColor': '#2563EB',
      'favoriteCount': 0,
    },
    {
      'categoryID': 'work',
      'userID': 'alice',
      'categoryName': '工作资料',
      'categoryColor': '#0F766E',
      'favoriteCount': 0,
    },
  ];
  SessionCredentials credentials(String user) => SessionCredentials(
    userId: user,
    tenantId: 'fixture-tenant',
    tenantName: '测试服务',
    domains: ['im.example.com'],
    chatToken: 'fixture-chat',
    imToken: 'fixture-im',
  );
  Future<void> start() async {
    await session.signIn((_) async => credentials('alice'));
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
    repository = FavoriteRepository(api);
  }

  AppRuntime get runtime => _runtime ??= AppRuntime(
    config: const AppConfig(),
    consent: ConsentMemory()..granted = true,
    routes: RouteConfigLoader(config: const AppConfig()),
    session: session,
    auth: (_, _) async => throw StateError('not used'),
  )..phase = AppPhase.online;
  Object? respond(RequestOptions request) {
    final operation = request.uri.path.replaceFirst('/api/favorite/', '');
    final body = Map<String, Object?>.from(request.data as Map);
    switch (operation) {
      case 'list':
      case 'search':
        final pagination = body['pagination'] as Map;
        final page = pagination['pageNumber'] as int,
            count = pagination['showNumber'] as int;
        final matches = items
            .where(
              (item) =>
                  (body['categoryID'] == null ||
                      item['categoryID'] == body['categoryID']) &&
                  (body['favoriteType'] == null ||
                      body['favoriteType'] == 0 ||
                      item['favoriteType'] == body['favoriteType']) &&
                  (body['sourceID'] == null ||
                      item['sourceID'] == body['sourceID']) &&
                  (body['conversationID'] == null ||
                      item['conversationID'] == body['conversationID']) &&
                  (body['keyword'] == null ||
                      '${item['title']} ${item['content']}'.contains(
                        body['keyword'] as String,
                      )),
            )
            .toList();
        return {
          'favorites': matches.skip((page - 1) * count).take(count).toList(),
          'total': matches.length,
          'hasMore': page * count < matches.length,
        };
      case 'category/list':
        return {'categories': categories};
      case 'stats':
        return {
          'totalCount': items.length,
          'todayAddedCount': items.length,
          'weekAddedCount': items.length,
          'monthAddedCount': items.length,
          'categoryCount': categories.length,
          'lastFavoriteTime': items.isEmpty ? 0 : items.last['createTime'],
          for (final entry in {
            1: 'messageCount',
            2: 'fileCount',
            3: 'imageCount',
            4: 'videoCount',
            5: 'audioCount',
            6: 'linkCount',
          }.entries)
            entry.value: items
                .where((item) => item['favoriteType'] == entry.key)
                .length,
          'categoriesWithCount': [
            for (final category in categories)
              {
                ...category,
                'favoriteCount': items
                    .where(
                      (item) => item['categoryID'] == category['categoryID'],
                    )
                    .length,
              },
          ],
        };
      case 'info':
        return {
          'favoriteInfo': items.firstWhere(
            (item) => item['favoriteID'] == body['favoriteID'],
          ),
        };
      case 'add':
        final value = {...item('added'), ...body, 'favoriteID': 'added'};
        items.add(value);
        return {'favoriteInfo': value};
      case 'update':
        final value = items.firstWhere(
          (item) => item['favoriteID'] == body['favoriteID'],
        );
        value.addAll(body);
        return {'favoriteInfo': value};
      case 'remove':
      case 'batch_operate':
        if (operation == 'remove' || body['operation'] == 4) {
          items.removeWhere(
            (item) =>
                (body['favoriteIDs'] as List).contains(item['favoriteID']),
          );
        }
        return {
          'success': true,
          'affected': (body['favoriteIDs'] as List).length,
        };
      case 'category/create':
        final category = {'categoryID': 'new-category', ...body};
        categories.add(category);
        return {'categoryInfo': category};
      case 'category/update':
        return {
          'categoryInfo': {...categories.last, ...body},
        };
      case 'category/delete':
        return {'success': true};
    }
    throw StateError('Unexpected fixture operation $operation');
  }

  static Map<String, Object?> item(
    String id, {
    String title = '设计资料',
    String content = '收藏正文',
    int kind = 1,
  }) => {
    'favoriteID': id,
    'userID': 'alice',
    'favoriteType': kind,
    'sourceID': 'source-$id',
    'conversationID': 'conversation-a',
    'title': title,
    'content': content,
    'categoryID': 'default',
    'tags': <String>[],
    'notes': '',
    'createTime': 1700000000000,
    'updateTime': 1700000000000,
  };
  Future<void> dispose() async {
    api.close();
    await session.dispose();
  }
}
