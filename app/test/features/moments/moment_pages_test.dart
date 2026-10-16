import 'dart:io';
import 'dart:ui' as ui;

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/im/open_im_gateway.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/contacts/domain/contact.dart';
import 'package:imapp/features/group/domain/group.dart';
import 'package:imapp/features/moments/domain/moment.dart';
import 'package:imapp/features/moments/presentation/moment_audience_page.dart';
import 'package:imapp/features/moments/presentation/moment_publish_page.dart';
import 'package:imapp/features/moments/presentation/moment_interactions_page.dart';
import 'package:imapp/features/moments/presentation/moments_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import 'moment_harness.dart';

void main() {
  late MomentHarness fixture;
  late OpenImGateway gateway;
  setUp(() {
    fixture = MomentHarness();
    gateway = OpenImGateway(hasPrivacyConsent: () => true);
  });
  Future<GlobalKey> mount(WidgetTester tester, Widget page) async {
    await tester.runAsync(fixture.start);
    tester.view.physicalSize = const Size(360, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final key = GlobalKey();
    final theme = await captureTheme(tester);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => fixture.runtime),
          tenantApiProvider.overrideWithValue(fixture.api),
          contactsRepositoryProvider.overrideWithValue(
            ContactsRepository(gateway, fixture.api.access, fixture.api),
          ),
          friendsProvider.overrideWith(
            (_) async => const [Contact(id: 'bob', name: '小满', friend: true)],
          ),
          joinedGroupsProvider.overrideWith(
            (_) async => const [
              GroupSummary(
                id: 'group-a',
                name: '周末徒步',
                ownerId: 'alice',
                members: 12,
                sessionType: 3,
              ),
            ],
          ),
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
    await tester.runAsync(() async {
      await fixture.dispose();
      await gateway.dispose();
    });
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
    'feed stays usable at large text and deletes only after confirmation',
    (tester) async {
      final key = await mount(tester, const MomentsPage());
      expect(find.text('晴空'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await capture(tester, key, 'moments-feed-widget');
      await tester.tap(find.byTooltip('删除动态'));
      await tester.pumpAndSettle();
      expect(fixture.items.length, 1);
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(fixture.items.length, 1);
      await tester.tap(find.byTooltip('删除动态'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(TextButton, '删除'));
      await tester.pumpAndSettle();
      expect(fixture.items, isEmpty);
      expect(find.text('还没有动态，分享一个新发现吧'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'comment failure keeps the draft and reply target, and successful retry refreshes detail',
    (tester) async {
      await mount(tester, const MomentDetailPage(id: 'one'));
      final comment = find.textContaining('记得一起去看看！');
      await tester.ensureVisible(comment);
      await tester.tap(comment);
      await tester.pumpAndSettle();
      expect(find.text('回复 小满'), findsOneWidget);
      await tester.enterText(find.byType(TextField), '明天见，保留我的回复');
      var fail = true;
      fixture.overrideRequest = (request) async {
        if (request.uri.path.endsWith('/comment/add') && fail) {
          throw DioException(
            requestOptions: request,
            type: DioExceptionType.connectionError,
          );
        }
        return fixture.respond(request);
      };
      await tester.tap(find.widgetWithText(TextButton, '确定'));
      await tester.pumpAndSettle();
      expect(find.text('明天见，保留我的回复'), findsOneWidget);
      expect(find.text('网络连接失败，请检查网络后重试'), findsOneWidget);
      fail = false;
      await tester.tap(find.widgetWithText(TextButton, '确定'));
      await tester.pumpAndSettle();
      expect(find.byType(AlertDialog), findsNothing);
      expect(find.textContaining('明天见，保留我的回复'), findsOneWidget);
      final sent = fixture.http.requests
          .where((request) => request.uri.path.endsWith('/comment/add'))
          .last;
      expect(sent.data['replyUserID'], 'bob');
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'uncertain publication preserves draft and blocks automatic or repeated submission',
    (tester) async {
      final key = await mount(tester, const MomentPublishPage());
      await capture(tester, key, 'moments-publish-widget');
      await tester.enterText(find.byType(TextField), '发布结果未知时保留的内容');
      fixture.overrideRequest = (request) async {
        if (request.uri.path.endsWith('/add')) {
          throw DioException(
            requestOptions: request,
            type: DioExceptionType.receiveTimeout,
          );
        }
        return fixture.respond(request);
      };
      await tester.tap(find.widgetWithText(TextButton, '发布'));
      await tester.pumpAndSettle();
      expect(find.text('发布结果未知时保留的内容'), findsOneWidget);
      expect(
        tester
            .widget<TextButton>(find.widgetWithText(TextButton, '发布'))
            .onPressed,
        isNull,
      );
      expect(
        fixture.http.requests.where((r) => r.uri.path.endsWith('/add')).length,
        1,
      );
      await tester.ensureVisible(find.textContaining('暂时无法确认发布结果'));
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'audience never silently becomes public; selected users and groups remain explicit',
    (tester) async {
      final key = await mount(
        tester,
        MomentAudiencePage(
          initial: MomentAudience(MomentVisibility.included, [], []),
        ),
      );
      expect(
        tester
            .widget<TextButton>(find.widgetWithText(TextButton, '完成'))
            .onPressed,
        isNull,
      );
      await tester.ensureVisible(find.text('小满'));
      await tester.tap(find.text('小满'));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<TextButton>(find.widgetWithText(TextButton, '完成'))
            .onPressed,
        isNotNull,
      );
      await tester.ensureVisible(find.text('周末徒步'));
      await tester.tap(find.text('周末徒步'));
      await tester.pumpAndSettle();
      expect(find.text('已选 1 位好友、1 个群；以发布时的群成员为准'), findsOneWidget);
      await tester.drag(find.byType(ListView), const Offset(0, 800));
      await tester.pumpAndSettle();
      await capture(tester, key, 'moments-audience-widget');
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'opening interactions reads the count, clearing requires confirmation and preserves posts',
    (tester) async {
      await mount(tester, const MomentInteractionsPage());
      expect(fixture.unread, 0);
      expect(fixture.items.length, 1);
      await tester.tap(find.text('清空'));
      await tester.pumpAndSettle();
      expect(find.text('清空互动消息？'), findsOneWidget);
      await tester.tap(find.widgetWithText(TextButton, '清空').last);
      await tester.pumpAndSettle();
      expect(find.text('还没有新的互动'), findsOneWidget);
      expect(fixture.items.length, 1);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
}
