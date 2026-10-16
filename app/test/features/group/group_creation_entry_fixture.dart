import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/contacts/presentation/contacts_page.dart';
import 'package:imapp/features/contacts/presentation/select_contacts_page.dart';
import 'package:imapp/features/conversation/presentation/conversations_page.dart';
import 'package:imapp/features/group/presentation/create_group_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';
import 'create_group_fixture.dart';

enum CreationSurface { inbox, chat, contacts }

/// Actual pages, selector, editor, repositories, gateway and method codec.
/// Only the pre-existing native SDK and HTTP transport seams are simulated.
final class GroupEntryFixture {
  final creation = CreationFixture();
  final verificationRequests = <RequestOptions>[];
  Object? verificationStatus = 1;
  Future<void> Function()? beforeVerification;
  late CreationSurface surface;
  final cleanups = <void Function()>[];

  Completer<void> deferred() {
    final value = Completer<void>();
    cleanups.add(() { if (!value.isCompleted) value.complete(); });
    return value;
  }

  Future<void> start({bool group = false}) async {
    await creation.start();
    final base = creation.base;
    base.api.close();
    final access = SessionAccess(base.sdk.session);
    base.api = TenantApi(access, dio: Dio()..httpClientAdapter = TestHttpAdapter((request) async {
      if (request.path.endsWith('/verification/status')) {
        verificationRequests.add(request);
        await beforeVerification?.call();
        return jsonResponse(jsonEncode({'errCode': 0, 'data': {'status': verificationStatus}}));
      }
      return jsonResponse('{"errCode":0,"data":{"users":[]}}');
    }));
    creation.contacts = ContactsRepository(base.sdk.gateway, access, base.api);
    base.sdk.conversations.add(SdkHarness.conversation(
      id: 'origin-cid', title: '同名好友', group: group,
    ));
  }

  Future<void> mount(WidgetTester tester, CreationSurface value, {
    bool group = false, double width = 360, double height = 640,
  }) async {
    surface = value;
    await tester.runAsync(() => start(group: group));
    tester.view.physicalSize = Size(width, height);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester);
    creation.router = GoRouter(initialLocation: value == CreationSurface.chat ? '/chat/origin-cid' : '/', routes: [
      GoRoute(path: '/', builder: (_, _) => value == CreationSurface.contacts
        ? const ContactsPage() : const ConversationsPage()),
      GoRoute(path: '/chat/:id', builder: (_, state) => state.pathParameters['id'] == 'origin-cid'
        ? const ChatPage(conversationId: 'origin-cid')
        : Scaffold(body: Text('accepted-chat:${state.pathParameters['id']}'))),
      GoRoute(path: '/contacts/groups', builder: (_, _) => const Scaffold(body: Text('group-list'))),
      GoRoute(path: '/blank', builder: (_, _) => const Scaffold(body: Text('blank-route'))),
    ]);
    await tester.pumpWidget(ProviderScope(overrides: [
      imGatewayProvider.overrideWithValue(creation.base.sdk.gateway),
      appRuntimeProvider.overrideWith((_) => creation.base.sdk.runtime),
      tenantApiProvider.overrideWithValue(creation.base.api),
      contactsRepositoryProvider.overrideWith((_) => creation.contacts),
      groupRepositoryProvider.overrideWith((_) => creation.repository),
    ], child: Builder(builder: (context) {
      creation.container = ProviderScope.containerOf(context, listen: false);
      return MaterialApp.router(theme: theme, routerConfig: creation.router,
        builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
          .copyWith(textScaler: TextScaler.linear(1.5)), child: child!));
    })));
    await creation.frame(tester);
    if (value == CreationSurface.chat) {
      await until(tester, () => find.byTooltip('聊天设置').evaluate().isNotEmpty &&
        find.text('同名好友').evaluate().isNotEmpty);
    }
  }

  Future<void> until(WidgetTester tester, bool Function() condition) => creation.until(tester, condition);
  Future<void> frame(WidgetTester tester) => creation.frame(tester);
  Future<void> settings(WidgetTester tester) async {
    await tester.tap(find.byTooltip('聊天设置'));
    await frame(tester);
  }

  Finder get entry => surface == CreationSurface.chat
    ? find.widgetWithText(ListTile, '创建群聊') : find.byTooltip('创建群聊');
  Future<void> open(WidgetTester tester) async {
    if (surface == CreationSurface.chat) await settings(tester);
    await tester.tap(entry);
    await until(tester, () => find.byType(SelectContactsPage).evaluate().isNotEmpty);
    await creation.readyRoute(tester, find.byType(SelectContactsPage));
    await until(tester, () => find.byType(CheckboxListTile).evaluate().isNotEmpty);
  }

  Future<void> select(WidgetTester tester, String id) async {
    final row = find.ancestor(of: find.text('ID：$id'), matching: find.byType(CheckboxListTile));
    await tester.ensureVisible(row);
    await tester.tap(row); await tester.pump();
  }

  Future<void> editor(WidgetTester tester) async {
    final next = find.widgetWithText(TextButton, '确定 (2)');
    await tester.tap(next);
    await until(tester, () => find.byType(CreateGroupPage).evaluate().isNotEmpty);
    await creation.readyRoute(tester, find.byType(CreateGroupPage));
  }

  Future<void> coverAndReturn(WidgetTester tester) => creation.coverAndReturn(tester);
  Future<void> close(WidgetTester tester) async {
    for (final finish in cleanups) { finish(); }
    await creation.close(tester);
  }
}
