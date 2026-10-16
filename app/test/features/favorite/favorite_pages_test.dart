import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/features/favorite/presentation/favorite_categories_page.dart';
import 'package:imapp/features/favorite/presentation/favorite_detail_page.dart';
import 'package:imapp/features/favorite/presentation/favorites_page.dart';
import 'package:imapp/features/favorite/presentation/favorite_stats_page.dart';
import 'package:imapp/features/favorite/presentation/favorite_forward_page.dart';
import 'package:imapp/features/favorite/domain/favorite.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import 'favorite_harness.dart';

void main() {
  late FavoriteHarness fixture;
  setUp(() {
    fixture = FavoriteHarness()..items.add(FavoriteHarness.item('one'));
  });
  Future<GlobalKey> mount(
    WidgetTester tester,
    Widget page, {
    bool start = true,
  }) async {
    if (start) await tester.runAsync(fixture.start);
    tester.view.physicalSize = const Size(360, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester);
    final key = GlobalKey();
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => fixture.runtime),
          tenantApiProvider.overrideWithValue(fixture.api),
        ],
        child: RepaintBoundary(
          key: key,
          child: MaterialApp(
            debugShowCheckedModeBanner: false,
            theme: theme,
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context)
                  .copyWith(textScaler: TextScaler.linear(1.6)),
              child: child!,
            ),
            home: page,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    return key;
  }

  Future<void> close(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
    await tester.runAsync(fixture.dispose);
  }

  Future<void> capture(WidgetTester tester, GlobalKey key, String name) async {
    const directory = String.fromEnvironment('TEST_CAPTURE_DIR');
    if (directory.isEmpty) return;
    await tester.runAsync(() async {
      final boundary =
          key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 2);
      final bytes = (await image.toByteData(format: ui.ImageByteFormat.png))!;
      await Directory(directory).create(recursive: true);
      await File('$directory/$name.png')
          .writeAsBytes(bytes.buffer.asUint8List());
      image.dispose();
    });
  }

  testWidgets(
    'server-isolated detail exposes only metadata and explicit delete without content editing sharing downloading or forwarding',
    (tester) async {
      fixture.items.single.addAll({
        'retentionBlocked': true,
        'title': 'must-not-display',
        'content': 'must-not-display-body',
        'notes': 'must-not-display-notes',
      });
      await mount(tester, const FavoriteDetailPage(favoriteId: 'one'));
      expect(find.text('私密收藏已隔离，可删除此记录'), findsOneWidget);
      expect(find.textContaining('must-not-display'), findsNothing);
      expect(find.text('删除收藏'), findsOneWidget);
      expect(find.byTooltip('导出此条收藏'), findsNothing);
      expect(find.text('转发到聊天'), findsNothing);
      expect(find.text('复制完整内容'), findsNothing);
      expect(find.text('下载并保存附件'), findsNothing);
      expect(find.text('标题'), findsNothing);
      await close(tester);
    },
  );
  testWidgets(
    'opening an old private favorite forwarding screen cannot list recipients or dispatch SDK work',
    (tester) async {
      await tester.runAsync(fixture.start);
      await mount(
        tester,
        FavoriteForwardPage(
          item: FavoriteItem(
            id: 'old-private',
            ownerId: 'alice',
            kind: FavoriteKind.message,
            sourceId: 'private-source',
            content: jsonEncode({
              'clientMsgID': 'private-source',
              'attachedInfoElem': {'isPrivateChat': true},
            }),
          ),
          repository: fixture.repository,
        ),
        start: false,
      );
      expect(find.textContaining('阅后即焚内容不能收藏或转发'), findsOneWidget);
      expect(find.byType(TextField), findsNothing);
      expect(fixture.http.requests, isEmpty);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'opening an old forwarding screen after account switch never initializes or lists the new account SDK',
    (tester) async {
      await tester.runAsync(() async {
        await fixture.start();
        await fixture.session.signOut();
        await fixture.session.signIn((_) async => fixture.credentials('bob'));
      });
      await mount(
        tester,
        FavoriteForwardPage(
          item: FavoriteItem.fromJson(fixture.items.single, 'alice'),
          repository: fixture.repository,
        ),
        start: false,
      );
      expect(find.text('登录已失效，请重新登录'), findsOneWidget);
      expect(find.byType(TextField), findsNothing);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'statistics show every type and category at large text, keep UTC periods explicit, and refresh safely when empty',
    (tester) async {
      final key = await mount(tester, const FavoriteStatsPage());
      expect(find.text('收藏总数'), findsOneWidget);
      expect(find.text('按 UTC 自然日、周、月统计；周一为每周起点。'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await capture(tester, key, 'favorite-stats-widget');
      await tester.scrollUntilVisible(find.text('链接'), 200);
      expect(find.text('0 条 · 0.0%'), findsWidgets);
      await tester.scrollUntilVisible(find.text('工作资料'), 200);
      expect(tester.takeException(), isNull);
      fixture.items.clear();
      await tester.tap(find.byTooltip('刷新统计'));
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(find.text('还没有收藏内容。'), -200);
      expect(find.text('还没有收藏内容。'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'favorites filters are scrollable at large text; batch deletion requires explicit confirmation',
    (tester) async {
      final key = await mount(tester, const FavoritesPage());
      expect(tester.takeException(), isNull);
      await capture(tester, key, 'favorites-widget');
      await tester.tap(find.text('选择'));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('设计资料'));
      await tester.tap(find.text('设计资料'));
      await tester.pumpAndSettle();
      expect(find.text('已选择 1 条'), findsOneWidget);
      await tester.tap(find.text('删除'));
      await tester.pumpAndSettle();
      expect(find.text('删除所选收藏？'), findsOneWidget);
      expect(
        fixture.http.requests.any(
          (request) => request.uri.path.endsWith('/batch_operate'),
        ),
        isFalse,
      );
      await tester.tap(find.text('取消').last);
      await tester.pumpAndSettle();
      expect(
        fixture.http.requests.any(
          (request) => request.uri.path.endsWith('/batch_operate'),
        ),
        isFalse,
      );
      await tester.tap(find.text('删除'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(TextButton, '删除').last);
      await tester.pumpAndSettle();
      expect(fixture.items, isEmpty);
      expect(find.text('我的收藏'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'failed metadata saves retain the draft and successful retry is the only path that closes the editor',
    (tester) async {
      await mount(tester, const FavoriteDetailPage(favoriteId: 'one'));
      final title = find.widgetWithText(ListTile, '标题');
      await tester.ensureVisible(title);
      await tester.tap(title);
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), '保存失败仍然保留的标题');
      var fail = true;
      fixture.overrideRequest = (request) async {
        if (request.uri.path.endsWith('/update') && fail) {
          throw DioException(
            requestOptions: request,
            type: DioExceptionType.connectionError,
          );
        }
        return fixture.respond(request);
      };
      await tester.tap(find.text('确定'));
      await tester.pumpAndSettle();
      expect(find.text('收藏标题'), findsOneWidget);
      expect(
        tester.widget<TextField>(find.byType(TextField)).controller!.text,
        '保存失败仍然保留的标题',
      );
      expect(find.text('网络连接失败，请检查网络后重试'), findsOneWidget);
      fail = false;
      await tester.tap(find.text('确定'));
      await tester.pumpAndSettle();
      expect(find.text('收藏标题'), findsNothing);
      expect(fixture.items.single['title'], '保存失败仍然保留的标题');
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'a slow save blocks dismissing the editor until the durable write completes',
    (tester) async {
      await mount(tester, const FavoriteDetailPage(favoriteId: 'one'));
      final title = find.widgetWithText(ListTile, '标题');
      await tester.ensureVisible(title);
      await tester.tap(title);
      await tester.pumpAndSettle();
      final result = Completer<Object?>();
      final started = Completer<void>();
      fixture.overrideRequest = (request) async {
        if (request.uri.path.endsWith('/update')) {
          started.complete();
          return result.future;
        }
        return fixture.respond(request);
      };
      await tester.enterText(find.byType(TextField), '等待服务端确认');
      await tester.tap(find.text('确定'));
      await tester.pump();
      for (var i = 0; i < 10 && !started.isCompleted; i++) {
        await tester.pump(const Duration(milliseconds: 10));
      }
      expect(started.isCompleted, isTrue);
      expect(
        tester
            .widget<TextButton>(find.widgetWithText(TextButton, '取消'))
            .onPressed,
        isNull,
      );
      expect(tester.widget<TextField>(find.byType(TextField)).enabled, isFalse);
      result.complete({
        'favoriteInfo': {...FavoriteHarness.item('one'), 'title': '等待服务端确认'},
      });
      await tester.pumpAndSettle();
      expect(find.text('收藏标题'), findsNothing);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'default category is rendered once without mutation controls and category creation keeps invalid input',
    (tester) async {
      await mount(tester, const FavoriteCategoriesPage());
      expect(find.text('默认分类'), findsOneWidget);
      expect(find.byType(PopupMenuButton<String>), findsOneWidget);
      await tester.tap(find.byTooltip('新建分类'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('确定'));
      await tester.pumpAndSettle();
      expect(find.text('新建分类'), findsOneWidget);
      expect(find.byType(TextField), findsOneWidget);
      expect(
        fixture.http.requests.any(
          (request) => request.uri.path.endsWith('/category/create'),
        ),
        isFalse,
      );
      await tester.enterText(find.byType(TextField), '新收藏分类');
      await tester.tap(find.text('确定'));
      await tester.pumpAndSettle();
      expect(find.byType(TextField), findsNothing);
      expect(
        fixture.http.requests
            .lastWhere(
              (request) => request.uri.path.endsWith('/category/create'),
            )
            .data['categoryName'],
        '新收藏分类',
      );
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
}
