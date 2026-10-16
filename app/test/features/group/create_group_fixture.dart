import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/core/network/tenant_api.dart' show tenantApiProvider;
import 'package:imapp/core/storage/object_uploader.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/contacts/domain/contact.dart';
import 'package:imapp/features/contacts/presentation/select_contacts_page.dart';
import 'package:imapp/features/group/data/group_repository.dart';
import 'package:imapp/features/group/presentation/create_group_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import 'group_avatar_fixture.dart' as avatar_tools;

const creationMembers = [
  Contact(id: 'bob', name: '同名好友', friend: true),
  Contact(id: 'carol', name: '同名好友', friend: true),
];

/// Actual SDK/repository/staging/uploader, with existing OS and HTTP transport
/// seams only. No business service or private account is contacted.
final class CreationFixture {
  final base = avatar_tools.AvatarFixture();
  late GroupRepository repository;
  late ContactsRepository contacts;
  GoRouter? router;
  late ProviderContainer container;
  final creates = <Map<String, dynamic>>[];
  final providerBodies = <List<int>>[];
  final providerRequests = <RequestOptions>[];
  Future<void> Function()? createReply;
  Future<String> Function()? conversationReply;
  Future<void> Function(int index)? uploadReply;
  bool started = false;
  final pending = <void Function()>[];

  Completer<T> defer<T>(T fallback) {
    final value = Completer<T>();
    pending.add(() { if (!value.isCompleted) value.complete(fallback); });
    return value;
  }
  Future<void> start() async {
    if (started) return;
    started = true;
    await base.start();
    repository = GroupRepository(base.sdk.gateway, base.api.access);
    contacts = ContactsRepository(base.sdk.gateway, base.api.access, base.api);
    final previous = base.sdk.overrideCall;
    base.sdk.overrideCall = (call) async {
      final args = call.arguments as Map;
      switch (call.method) {
        case 'getFriendList':
          return jsonEncode([for (final m in creationMembers)
            {'friendInfo': {'userID': m.id, 'nickname': m.name}}]);
        case 'createGroup':
          creates.add(Map<String, dynamic>.from(args));
          await createReply?.call();
          return jsonEncode({'groupID': 'created-group', 'groupType': 2,
            'groupName': (args['groupInfo'] as Map)['groupName'],
            'ownerUserID': base.sdk.userId, 'memberCount': 3});
        case 'getOneConversation':
          if (conversationReply != null) return await conversationReply!();
          final group = args['sessionType'] != 1;
          return jsonEncode({...SdkHarness.conversation(id: group ? 'created-cid' : 'single-cid', group: group),
            'groupID': group ? args['sourceID'] : '', 'userID': group ? '' : args['sourceID']});
      }
      return previous == null ? base.sdk.respond(call) : await previous(call);
    };
  }
  Future<void> mount(WidgetTester tester, {bool entry = false, double width = 360, double height = 640}) async {
    await tester.runAsync(start);
    tester.view.physicalSize = Size(width, height); tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester);
    router = GoRouter(initialLocation: entry ? '/' : '/create', routes: [
      GoRoute(path: '/', builder: (context, _) => Scaffold(body: Center(child: FilledButton(
        onPressed: () => startConversation(context), child: const Text('发起聊天'))))),
      GoRoute(path: '/create', builder: (_, _) => CreateGroupPage(members: creationMembers, owner: repository)),
      GoRoute(path: '/chat/:id', builder: (_, s) => Scaffold(body: Text('chat:${s.pathParameters['id']}'))),
      GoRoute(path: '/contacts/groups', builder: (_, _) => const Scaffold(body: Text('authoritative-group-list'))),
      GoRoute(path: '/contacts/groups/:id', builder: (_, s) => Scaffold(body: Text('detail:${s.pathParameters['id']}'))),
    ]);
    await tester.pumpWidget(ProviderScope(overrides: [
      imGatewayProvider.overrideWithValue(base.sdk.gateway),
      appRuntimeProvider.overrideWith((_) => base.sdk.runtime),
      tenantApiProvider.overrideWithValue(base.api),
      groupRepositoryProvider.overrideWith((_) => repository),
      contactsRepositoryProvider.overrideWith((_) => contacts),
      groupCreateAvatarPickerProvider.overrideWithValue((source) async {
        base.sources.add(source);
        return base.picker == null ? XFile(base.original.path) : await base.picker!(source);
      }),
      groupCreateAvatarUploaderProvider.overrideWithValue((api) => ObjectUploader(api,
        storage: Dio()..httpClientAdapter = _CreationStorage(this))),
    ], child: Builder(builder: (context) {
      container = ProviderScope.containerOf(context, listen: false);
      return MaterialApp.router(routerConfig: router, theme: theme,
        builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
          .copyWith(textScaler: TextScaler.linear(1.5)), child: child!));
    })));
    await frame(tester);
  }
  Future<void> frame(WidgetTester tester) => avatar_tools.routeFrame(tester);
  Future<void> until(WidgetTester tester, bool Function() condition) => avatar_tools.until(tester, condition);
  Future<void> tap(WidgetTester tester, String text) async {
    final finder = text == '创建群聊' || text == '重试打开会话'
      ? find.widgetWithText(FilledButton, text) : find.text(text);
    final list = find.byType(ListView);
    if (list.evaluate().isNotEmpty) {
      await tester.scrollUntilVisible(finder, 80,
        scrollable: find.descendant(of: list.last, matching: find.byType(Scrollable)).first);
      await tester.pump();
    }
    await tester.tap(finder); await frame(tester);
  }
  Future<void> preview(WidgetTester tester) async {
    final pick = find.byKey(const ValueKey('create-group-avatar'));
    expect(pick, findsOneWidget);
    await tester.ensureVisible(pick); await tester.pump();
    await tester.tap(pick); await frame(tester);
    await tester.tap(find.text('从相册选择')); await tester.pump();
    await until(tester, () => find.text('使用这张群头像？').evaluate().isNotEmpty);
  }
  Future<void> upload(WidgetTester tester) async {
    await preview(tester);
    final oldCount = providerBodies.length;
    await tester.tap(find.widgetWithText(FilledButton, '使用')); await tester.pump();
    await until(tester, () => providerBodies.length > oldCount);
  }
  Future<void> avatarReady(WidgetTester tester) => until(tester, () {
    final create = find.widgetWithText(FilledButton, '创建群聊');
    return find.text('更换群头像').evaluate().isNotEmpty && create.evaluate().isNotEmpty &&
      tester.widget<FilledButton>(create).onPressed != null;
  });
  Future<void> coverAndReturn(WidgetTester tester) async {
    final nav = tester.state<NavigatorState>(find.byType(Navigator).last);
    unawaited(nav.push<void>(MaterialPageRoute(builder: (_) => const Scaffold(body: Text('foreign-route')))));
    await frame(tester); nav.pop(); await frame(tester);
  }
  Future<void> replaceOwner() async {
    await base.signInOther(user: 'other');
    final access = SessionAccess(base.sdk.session);
    repository = GroupRepository(base.sdk.gateway, access);
    // Owner replacement cases never call this old API: they must stop at
    // selection/native admission. OS/HTTP ports stay the same explicit mocks.
    contacts = ContactsRepository(base.sdk.gateway, access, base.api);
    container.invalidate(groupRepositoryProvider);
    container.invalidate(contactsRepositoryProvider);
  }
  Future<void> close(WidgetTester tester) async {
    for (final settle in pending) { settle(); }
    await base.close(tester);
    router?.dispose();
  }
}

final class _CreationStorage implements HttpClientAdapter {
  _CreationStorage(this.fixture);
  final CreationFixture fixture;
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? stream, Future<void>? cancelFuture) async {
    fixture.providerRequests.add(options);
    fixture.providerBodies.add(await stream!.fold<List<int>>([], (a, b) => a..addAll(b)));
    await fixture.uploadReply?.call(fixture.providerBodies.length);
    return ResponseBody.fromBytes([], 204);
  }
  @override
  void close({bool force = false}) {}
}
