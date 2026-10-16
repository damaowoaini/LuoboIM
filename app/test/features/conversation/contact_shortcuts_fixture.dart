import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/contacts/presentation/contact_search_page.dart';
import 'package:imapp/features/conversation/presentation/conversations_page.dart';

import '../../support/http_adapter.dart';
import '../group/group_creation_entry_fixture.dart';

/// The production router, inbox, search page, repositories, session and gateway
/// are real. Only existing native MethodChannel and HTTP transport seams use
/// the accepted synthetic fixture. No SDK write or business service is needed.
final class ContactShortcutsFixture {
  final entry = GroupEntryFixture();
  late TestHttpAdapter http;
  TenantApi? lookupApi;
  Future<ResponseBody> Function(RequestOptions)? reply;
  final pending = <void Function()>[];

  GoRouter get router => entry.creation.router!;
  ProviderContainer get container => entry.creation.container;
  int get sdkWrites => entry.creation.base.sdk.calls.where((call) => const {
    'addFriend', 'joinGroup', 'createGroup', 'sendMessage',
  }.contains(call.method)).length;

  ResponseBody response(List<Map<String, Object?>> rows) => jsonResponse(
    jsonEncode({'errCode': 0, 'data': {'users': rows, 'total': rows.length}}),
  );

  Completer<ResponseBody> deferred() {
    final value = Completer<ResponseBody>();
    pending.add(() { if (!value.isCompleted) value.complete(response([])); });
    return value;
  }

  Future<void> mount(WidgetTester tester, {double width = 360, double height = 640}) async {
    await entry.mount(tester, CreationSurface.inbox, width: width,
      height: height, productionRouter: true);
    http = TestHttpAdapter((request) async {
      if (reply != null) return await reply!(request);
      final data = request.data as Map;
      final ids = data['userIDs'] as List?;
      final id = ids?.single as String? ?? 'bob';
      return response([{'userID': id, 'nickname': '完整资料$id', 'allowAddFriend': 1}]);
    });
    lookupApi = TenantApi(SessionAccess(entry.creation.base.sdk.session),
      dio: Dio()..httpClientAdapter = http);
    entry.creation.contacts = ContactsRepository(entry.creation.base.sdk.gateway,
      lookupApi!.access, lookupApi!);
    final previous = entry.creation.base.sdk.overrideCall!;
    entry.creation.base.sdk.overrideCall = (call) async {
      if (call.method == 'getUsersInfo') {
        final ids = (call.arguments as Map)['userIDList'] as List;
        return jsonEncode([for (final id in ids)
          {'publicInfo': {'userID': id, 'nickname': 'SDK资料$id'}}]);
      }
      return await previous(call);
    };
    container.invalidate(contactsRepositoryProvider);
    await entry.frame(tester);
    await ready(tester, find.byType(ConversationsPage));
  }

  Future<void> frame(WidgetTester tester) => entry.frame(tester);
  Future<void> until(WidgetTester tester, bool Function() condition) => entry.until(tester, condition);
  Future<void> ready(WidgetTester tester, Finder finder) => entry.creation.readyRoute(tester, finder);

  Future<void> menu(WidgetTester tester) async {
    final page = find.byType(ConversationsPage);
    await ready(tester, page);
    final more = find.descendant(of: page, matching: find.byTooltip('更多操作')).hitTestable();
    expect(more, findsOneWidget);
    await tester.tap(more);
    await until(tester, () => find.text('会议（预约与管理）').hitTestable().evaluate().length == 1);
    await ready(tester, find.text('会议（预约与管理）'));
  }

  Future<void> choose(WidgetTester tester, String text) async {
    await menu(tester);
    final item = find.text(text).hitTestable();
    expect(item, findsOneWidget);
    await tester.tap(item);
    await until(tester, () => find.byType(ContactSearchPage).evaluate().length == 1);
    await ready(tester, find.byType(ContactSearchPage));
  }

  Future<void> search(WidgetTester tester, String value) async {
    await tester.enterText(find.byType(TextField), value);
    await tester.tap(find.widgetWithText(FilledButton, '搜索').hitTestable());
    await frame(tester);
  }

  Future<void> back(WidgetTester tester) async {
    final page = find.byType(ContactSearchPage);
    await ready(tester, page);
    final route = ModalRoute.of(tester.element(page))!;
    await tester.tap(find.descendant(of: page, matching: find.byType(BackButton)).hitTestable());
    await entry.creation.removedRoute(tester, route);
    await ready(tester, find.byType(ConversationsPage));
  }

  Future<void> coverAndReturn(WidgetTester tester) async {
    final context = tester.element(find.byType(ConversationsPage));
    final foreign = MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('foreign-shortcut-route')));
    unawaited(Navigator.of(context).push(foreign));
    await until(tester, () => find.text('foreign-shortcut-route').evaluate().isNotEmpty);
    await ready(tester, find.text('foreign-shortcut-route'));
    Navigator.of(tester.element(find.text('foreign-shortcut-route'))).pop();
    await entry.creation.removedRoute(tester, foreign);
    await frame(tester);
  }

  Future<void> close(WidgetTester tester) async {
    for (final finish in pending) { finish(); }
    lookupApi?.close();
    await entry.close(tester);
  }
}
