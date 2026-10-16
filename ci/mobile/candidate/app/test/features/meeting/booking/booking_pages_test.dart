import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/features/meeting/booking/booking_providers.dart';
import 'package:imapp/features/meeting/booking/data/booking_contract.dart';
import 'package:imapp/features/meeting/booking/domain/booking_view.dart';
import 'package:imapp/features/meeting/booking/presentation/bookings_page.dart';
import 'package:imapp/features/meeting/booking/presentation/booking_editor_page.dart';
import 'package:imapp/features/meeting/booking/presentation/booking_detail_page.dart';

import 'booking_test_harness.dart';

void main() {
  late BookingFixture f;
  setUp(() { f = BookingFixture(); });
  tearDown(() async => f.close());
  Future<void> flush(WidgetTester tester) async {
    // The real journal query queues after the page's already-dispatched SQLite
    // observe, rather than a fixed sleep pretending to prove async completion.
    await tester.runAsync(() async { await f.repository.journal.pending(); }); await tester.pumpAndSettle();
  }
  Future<void> mount(WidgetTester tester, Widget child) async {
    await tester.pumpWidget(ProviderScope(overrides: [bookingControllerProvider.overrideWith((_) async => f.controller)],
      child: MaterialApp(home: child))); await tester.pump(); await flush(tester);
  }
  Future<void> close(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox.shrink()); await tester.runAsync(f.close);
  }
  Future<GoRouter> mountRoutedEditor(WidgetTester tester, {String initialLocation = '/meetings/bookings/new'}) async {
    final router = GoRouter(initialLocation: initialLocation, routes: [
      GoRoute(path: '/meetings/bookings', builder: (_, _) => const BookingsPage()),
      GoRoute(path: '/meetings/bookings/new', builder: (_, _) => const BookingEditorPage()),
      GoRoute(path: '/meetings/bookings/:bookingID', builder: (_, state) =>
        BookingDetailPage(bookingID: state.pathParameters['bookingID']!)),
    ]);
    await tester.pumpWidget(ProviderScope(overrides: [bookingControllerProvider.overrideWith((_) async => f.controller)],
      child: MaterialApp.router(routerConfig: router)));
    await tester.pump(); await flush(tester); return router;
  }
  Future<void> finishRecovery(WidgetTester tester, String action, Future<void> Function() invoke) async {
    final before = f.paths.where((x) => x == action).length, settled = Completer<void>();
    void changed() {
      if (!f.controller.isBusy('create') && !f.controller.unknownIDs.any(f.controller.isBusy) &&
          f.paths.where((x) => x == action).length > before && !settled.isCompleted) settled.complete();
    }
    f.controller.addListener(changed);
    try {
      await invoke(); await tester.runAsync(() => settled.future.timeout(const Duration(seconds: 3)));
      await tester.pump(); await flush(tester); expect(f.paths.where((x) => x == action), hasLength(before + 1));
    } finally { f.controller.removeListener(changed); }
  }
  Future<String> freezeEditor(WidgetTester tester, {required bool committed}) async {
    if (committed) { f.loseReply = true; }
    else { f.intercept = (action, body) async {
      if (action == 'create') throw StateError('synthetic request dropped before fixture commit');
      return f.respond(action, body);
    }; }
    await tester.enterText(find.byType(TextField).first, '原冻结草稿');
    await finishRecovery(tester, 'create', () async {
      final save = find.widgetWithText(FilledButton, '保存预约'); await tester.ensureVisible(save); await tester.tap(save);
    });
    expect(f.paths.where((x) => x == 'create'), hasLength(1));
    return f.requests[f.paths.indexOf('create')]['clientRequestID']! as String;
  }
  Future<void> resolveOriginal(WidgetTester tester) async {
    await finishRecovery(tester, 'resolve', () async {
      final resolve = find.widgetWithText(TextButton, '核实并关闭原意图');
      await tester.ensureVisible(resolve); await tester.tap(resolve); await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, '核实原意图'));
    });
  }
  testWidgets('unsupported service is visible instead of a successful empty list and cannot create', (tester) async {
    await tester.runAsync(() => f.open(released: false)); await mount(tester, const BookingsPage());
    expect(find.text('当前服务暂不支持预约。已有待确认操作会保留。'), findsOneWidget);
    expect(find.text('暂无匹配的已加载预约。'), findsNothing); expect(f.paths, isEmpty);
    final create = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '预约会议'));
    expect(create.onPressed, isNull); await close(tester);
  });
  testWidgets('actual editor validates before any intent dispatch and retains persistent field labels', (tester) async {
    await tester.runAsync(f.open); await mount(tester, const BookingEditorPage());
    await tester.tap(find.text('保存预约')); await tester.pumpAndSettle();
    expect(find.text('请检查主题、开始时间与时长。'), findsOneWidget);
    expect(find.text('会议主题'), findsOneWidget); expect(find.text('开始时间（本地时区）'), findsOneWidget);
    expect(f.paths.where((x) => x == 'create'), isEmpty); await close(tester);
  });
  testWidgets('an actual lost reply freezes the original editor and exposes recovery without second creation', (tester) async {
    await tester.runAsync(f.open); f.loseReply = true;
    final settled = Completer<void>();
    f.controller.addListener(() { if (!f.controller.isBusy('create') && f.paths.contains('create') && !settled.isCompleted) settled.complete(); });
    await mount(tester, const BookingEditorPage()); await tester.enterText(find.byType(TextField).first, '合成预约');
    await tester.tap(find.text('保存预约')); await tester.runAsync(() => settled.future.timeout(const Duration(seconds: 3)));
    await tester.pumpAndSettle();
    expect(f.paths.where((x) => x == 'create'), hasLength(1));
    expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存预约')).onPressed, isNull);
    expect(find.text('查询原回执'), findsOneWidget); expect(find.text('重试原操作'), findsOneWidget);
    await close(tester);
  });
  testWidgets('cancel dialog dismissal is zero write and confirmation carries exact ID/version/intent', (tester) async {
    await tester.runAsync(f.open); f.rows[bookingOne] = f.record();
    await mount(tester, const BookingDetailPage(bookingID: bookingOne)); await flush(tester);
    await tester.tap(find.text('取消预约')); await tester.pumpAndSettle(); await tester.tap(find.text('返回')); await tester.pumpAndSettle();
    expect(f.paths.where((x) => x == 'cancel'), isEmpty);
    final settled = Completer<void>(); f.controller.addListener(() {
      if (!f.controller.isBusy(bookingOne) && f.paths.contains('cancel') && !settled.isCompleted) settled.complete();
    });
    await tester.tap(find.text('取消预约')); await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '取消预约'));
    await tester.runAsync(() => settled.future.timeout(const Duration(seconds: 3))); await flush(tester);
    final index = f.paths.indexOf('cancel'), body = f.requests[index];
    expect(body['bookingID'], bookingOne); expect(body['expectedVersion'], 1); expect(body['clientRequestID'], isNotEmpty);
    expect(f.paths.where((x) => x == 'cancel'), hasLength(1)); expect(find.text('已取消预约'), findsOneWidget);
    await close(tester);
  });
  testWidgets('retiring the captured owner clears an open editor and leaves no callable save', (tester) async {
    await tester.runAsync(f.open); await mount(tester, const BookingEditorPage());
    await tester.enterText(find.byType(TextField).first, 'old-owner-draft');
    f.current = false; f.invalidations.add(2); await tester.pumpAndSettle();
    expect(find.text('old-owner-draft'), findsNothing);
    expect(find.text('保存预约'), findsNothing);
    expect(f.paths.where((x) => x == 'create'), isEmpty); await close(tester);
  });
  testWidgets('a captured recovery callback after its actual panel unmounts cannot dispatch a retry', (tester) async {
    await tester.runAsync(f.open); f.loseReply = true;
    await tester.runAsync(() => f.controller.submit(f.intent()));
    expect(f.paths.where((x) => x == 'create'), hasLength(1));
    await mount(tester, const BookingsPage());
    final retry = tester.widget<TextButton>(find.widgetWithText(TextButton, '重试原操作')).onPressed!;
    final before = List<String>.of(f.paths);
    await tester.pumpWidget(const SizedBox.shrink()); retry();
    await tester.runAsync(() => f.repository.journal.pending()); await tester.pump();
    expect(f.paths, before); await close(tester);
  });
  testWidgets('an actual detail target replacement rejects the captured old cancel callback', (tester) async {
    const otherID = '20000000-0000-4000-8000-000000000002';
    await tester.runAsync(f.open); f.rows[bookingOne] = f.record(); f.rows[otherID] = f.record(id: otherID);
    final page = ValueNotifier<Widget>(const BookingDetailPage(key: ValueKey('detail'), bookingID: bookingOne));
    await mount(tester, ValueListenableBuilder<Widget>(valueListenable: page, builder: (_, value, _) => value));
    await flush(tester);
    expect(f.requests.where((body) => body['bookingID'] == bookingOne), isNotEmpty);
    final cancel = tester.widget<OutlinedButton>(find.widgetWithText(OutlinedButton, '取消预约')).onPressed!;
    page.value = const BookingDetailPage(key: ValueKey('detail'), bookingID: otherID);
    await tester.pump(); await flush(tester); cancel(); await tester.pumpAndSettle();
    expect(find.text('取消此预约？'), findsNothing); expect(f.paths.where((x) => x == 'cancel'), isEmpty);
    expect(find.text('预约号：$otherID'), findsOneWidget);
    await close(tester); page.dispose();
  });
  testWidgets('reusing the actual editor State for a different target hides the original draft and blocks its save', (tester) async {
    const otherID = '20000000-0000-4000-8000-000000000002';
    await tester.runAsync(f.open);
    final first = OwnerBookingView.fromRecord(BookingRecord.wire(f.record(), 'owner'), 'owner');
    final second = OwnerBookingView.fromRecord(BookingRecord.wire(f.record(id: otherID), 'owner'), 'owner');
    final page = ValueNotifier<Widget>(BookingEditorPage(key: const ValueKey('editor'), original: first));
    await mount(tester, ValueListenableBuilder<Widget>(valueListenable: page, builder: (_, value, _) => value));
    final save = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存预约')).onPressed!;
    page.value = BookingEditorPage(key: const ValueKey('editor'), original: second);
    await tester.pumpAndSettle(); save(); await tester.pumpAndSettle();
    expect(find.text('登录、目标或访问状态已变化，请重新打开编辑页。'), findsOneWidget);
    expect(find.byType(TextField), findsNothing); expect(f.paths.where((x) => x == 'update'), isEmpty);
    await close(tester); page.dispose();
  });
  testWidgets('definitive not-applied recovery offers explicit refill without replaying or generating a second intent', (tester) async {
    await tester.runAsync(f.open); final router = await mountRoutedEditor(tester);
    final id = await freezeEditor(tester, committed: false); expect(f.rows, isEmpty);
    await resolveOriginal(tester);
    expect((await tester.runAsync(() => f.repository.journal.read(id)))?.receipt?.definitiveNotApplied, isTrue);
    expect(find.text('重新填写'), findsOneWidget); expect(find.text('查看详情'), findsNothing);
    final refill = find.widgetWithText(FilledButton, '重新填写'); await tester.ensureVisible(refill); await tester.tap(refill);
    await tester.pumpAndSettle();
    expect(tester.widget<TextField>(find.byType(TextField).first).enabled, isTrue);
    expect(find.text('原冻结草稿'), findsNothing);
    expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存预约')).onPressed, isNotNull);
    expect(f.paths.where((x) => x == 'create'), hasLength(1));
    expect(f.requests.where((body) => body.containsKey('clientRequestID')).every((body) => body['clientRequestID'] == id), isTrue);
    await close(tester); router.dispose();
  });
  testWidgets('applied recovery opens actual detail read and never treats the old receipt body as current detail', (tester) async {
    await tester.runAsync(f.open); final router = await mountRoutedEditor(tester);
    final id = await freezeEditor(tester, committed: true);
    f.rows[bookingOne] = {...f.record(version: 2), 'payload': f.payload(name: '权威详情主题')};
    await finishRecovery(tester, 'receipt', () async {
      final query = find.widgetWithText(TextButton, '查询原回执'); await tester.ensureVisible(query); await tester.tap(query);
    });
    expect(find.text('查看详情'), findsOneWidget); expect(find.text('重新填写'), findsNothing);
    expect(find.byType(TextField), findsNothing); expect(f.paths.where((x) => x == 'detail'), isEmpty);
    final proof = await tester.runAsync(() => f.repository.journal.read(id));
    expect(proof?.receipt?.record?.detailsAvailable, isFalse); expect(proof?.receipt?.record?.payload, isNull);
    final detail = find.widgetWithText(FilledButton, '查看详情'); await tester.ensureVisible(detail); await tester.tap(detail);
    await tester.pumpAndSettle(); await flush(tester);
    expect(router.routeInformationProvider.value.uri.path, '/meetings/bookings/$bookingOne');
    expect(f.paths.where((x) => x == 'detail'), hasLength(1));
    expect(find.text('权威详情主题'), findsOneWidget); expect(find.text('原冻结草稿'), findsNothing);
    expect(f.paths.where((x) => x == 'create'), hasLength(1));
    await close(tester); router.dispose();
  });
  testWidgets('not-found and failed receipt queries keep the real editor frozen with the same original UUID', (tester) async {
    await tester.runAsync(f.open); final router = await mountRoutedEditor(tester);
    final id = await freezeEditor(tester, committed: false);
    Future<void> query() => finishRecovery(tester, 'receipt', () async {
      final button = find.widgetWithText(TextButton, '查询原回执'); await tester.ensureVisible(button); await tester.tap(button);
    });
    await query(); expect(find.text('重新填写'), findsNothing); expect(find.text('查看详情'), findsNothing);
    f.intercept = (action, body) async {
      if (action == 'receipt') throw StateError('synthetic unavailable receipt');
      return f.respond(action, body);
    };
    await query();
    expect(find.text('重新填写'), findsNothing); expect(find.text('查看详情'), findsNothing);
    expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存预约')).onPressed, isNull);
    expect(f.controller.unknownIDs, [id]); expect(f.paths.where((x) => x == 'create'), hasLength(1));
    expect(f.requests.where((body) => body.containsKey('clientRequestID')).every((body) => body['clientRequestID'] == id), isTrue);
    await close(tester); router.dispose();
  });
  testWidgets('a terminal refill callback captured before owner retirement cannot unlock or navigate another scope', (tester) async {
    await tester.runAsync(f.open); final router = await mountRoutedEditor(tester);
    await freezeEditor(tester, committed: false); await resolveOriginal(tester);
    final refill = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '重新填写')).onPressed!;
    final before = List<String>.of(f.paths), path = router.routeInformationProvider.value.uri.path;
    f.current = false; f.invalidations.add(2); await tester.pumpAndSettle(); refill(); await tester.pumpAndSettle();
    expect(find.text('保存预约'), findsNothing); expect(find.text('重新填写'), findsNothing);
    expect(router.routeInformationProvider.value.uri.path, path); expect(f.paths, before);
    await close(tester); router.dispose();
  });
  testWidgets('direct new-route normal create opens an authoritative detail without needing any page to pop', (tester) async {
    await tester.runAsync(f.open); final router = await mountRoutedEditor(tester);
    expect(router.canPop(), isFalse);
    await tester.enterText(find.byType(TextField).first, '直达新建预约');
    await finishRecovery(tester, 'create', () async {
      final save = find.widgetWithText(FilledButton, '保存预约'); await tester.ensureVisible(save); await tester.tap(save);
    });
    expect(router.routeInformationProvider.value.uri.path, '/meetings/bookings/$bookingOne');
    expect(router.canPop(), isFalse); expect(find.byType(BookingEditorPage), findsNothing);
    expect(f.paths.where((x) => x == 'detail'), hasLength(1)); expect(find.text('直达新建预约'), findsOneWidget);
    expect(find.text('返回预约列表'), findsOneWidget); expect(f.paths.where((x) => x == 'create'), hasLength(1));
    await close(tester); router.dispose();
  });
  testWidgets('normal list-create-detail-list navigation rereads the authoritative list and displays the newly saved row', (tester) async {
    await tester.runAsync(f.open);
    final router = await mountRoutedEditor(tester, initialLocation: '/meetings/bookings');
    expect(f.controller.records, isEmpty);
    final create = find.widgetWithText(FilledButton, '预约会议'); await tester.ensureVisible(create); await tester.tap(create);
    await tester.pumpAndSettle(); await tester.enterText(find.byType(TextField).first, '从列表新建');
    await finishRecovery(tester, 'create', () async {
      final save = find.widgetWithText(FilledButton, '保存预约'); await tester.ensureVisible(save); await tester.tap(save);
    });
    expect(router.routeInformationProvider.value.uri.path, '/meetings/bookings/$bookingOne');
    final beforeReads = f.paths.where((x) => x == 'list').length;
    final back = find.widgetWithText(TextButton, '返回预约列表'); await tester.ensureVisible(back); await tester.tap(back);
    await tester.pump(); await flush(tester);
    expect(router.routeInformationProvider.value.uri.path, '/meetings/bookings');
    expect(f.paths.where((x) => x == 'list'), hasLength(beforeReads + 1));
    expect(f.controller.records.single.id.value, bookingOne); expect(find.text('从列表新建'), findsOneWidget);
    expect(f.paths.where((x) => x == 'create'), hasLength(1)); await close(tester); router.dispose();
  });
  testWidgets('normal detail pageless editor update pops only that editor and the existing parent rereads detail', (tester) async {
    await tester.runAsync(f.open); f.rows[bookingOne] = f.record();
    final router = await mountRoutedEditor(tester, initialLocation: '/meetings/bookings/$bookingOne');
    expect(f.paths.where((x) => x == 'detail'), hasLength(1));
    final edit = find.widgetWithText(OutlinedButton, '编辑主题与时间'); await tester.ensureVisible(edit); await tester.tap(edit);
    await tester.pumpAndSettle(); expect(find.byType(BookingEditorPage), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, '编辑后权威主题');
    await finishRecovery(tester, 'update', () async {
      final save = find.widgetWithText(FilledButton, '保存预约'); await tester.ensureVisible(save); await tester.tap(save);
    });
    expect(router.routeInformationProvider.value.uri.path, '/meetings/bookings/$bookingOne');
    expect(find.byType(BookingEditorPage), findsNothing); expect(f.paths.where((x) => x == 'detail'), hasLength(2));
    expect(find.text('编辑后权威主题'), findsOneWidget); expect(f.paths.where((x) => x == 'update'), hasLength(1));
    await close(tester); router.dispose();
  });
  testWidgets('direct detail has an explicit list route and authoritative read even with no navigation back stack', (tester) async {
    await tester.runAsync(f.open); f.rows[bookingOne] = f.record();
    final router = await mountRoutedEditor(tester, initialLocation: '/meetings/bookings/$bookingOne');
    expect(router.canPop(), isFalse); expect(f.controller.records, isEmpty);
    final beforeReads = f.paths.where((x) => x == 'list').length;
    final back = find.widgetWithText(TextButton, '返回预约列表'); await tester.ensureVisible(back); await tester.tap(back);
    await tester.pump(); await flush(tester);
    expect(router.routeInformationProvider.value.uri.path, '/meetings/bookings');
    expect(f.paths.where((x) => x == 'list'), hasLength(beforeReads + 1));
    expect(f.controller.records.single.id.value, bookingOne); expect(find.text('合成预约'), findsOneWidget);
    expect(f.paths.where((x) => ['create', 'update', 'cancel'].contains(x)), isEmpty);
    await close(tester); router.dispose();
  });
  testWidgets('entry read settling under the actual recovery dialog clears internal loading when that dialog closes', (tester) async {
    await tester.runAsync(f.open); f.rows[bookingOne] = f.record();
    f.intercept = (action, body) async {
      if (action == 'update') throw StateError('synthetic update dropped before fixture commit');
      return f.respond(action, body);
    };
    final pending = BookingIntent(id: intentOne, operation: BookingOperation.update,
      frozenBody: {'clientRequestID': intentOne, ...f.payload(), 'bookingID': bookingOne, 'expectedVersion': 1});
    await tester.runAsync(() => f.controller.submit(pending));
    expect(f.paths.where((x) => x == 'update'), hasLength(1)); expect(f.controller.canCreate, isTrue);
    final entered = Completer<void>(), reply = Completer<Object?>();
    f.intercept = (action, body) async {
      if (action == 'list') { entered.complete(); return reply.future; }
      return f.respond(action, body);
    };
    await tester.pumpWidget(ProviderScope(overrides: [bookingControllerProvider.overrideWith((_) async => f.controller)],
      child: const MaterialApp(home: BookingsPage())));
    await tester.pump(); await tester.runAsync(() => entered.future.timeout(const Duration(seconds: 3)));
    expect(f.controller.loading, isTrue);
    final resolve = find.widgetWithText(TextButton, '核实并关闭原意图'); await tester.ensureVisible(resolve); await tester.tap(resolve);
    await tester.pump(); await tester.pump(const Duration(milliseconds: 300));
    expect(find.text('核实并关闭原未知意图？'), findsOneWidget);
    final settled = Completer<void>();
    void changed() { if (!f.controller.loading && !settled.isCompleted) settled.complete(); }
    f.controller.addListener(changed);
    try {
      reply.complete({'bookings': [f.record()], 'hasMore': false});
      await tester.runAsync(() => settled.future.timeout(const Duration(seconds: 3))); await tester.pump();
    } finally { f.controller.removeListener(changed); }
    expect(f.controller.loading, isFalse);
    await tester.tap(find.widgetWithText(TextButton, '返回')); await tester.pumpAndSettle();
    expect(find.byType(LinearProgressIndicator), findsNothing); expect(find.text('合成预约'), findsOneWidget);
    expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '预约会议')).onPressed, isNotNull);
    expect(f.paths.where((x) => x == 'resolve'), isEmpty); expect(f.paths.where((x) => x == 'create'), isEmpty);
    expect(f.paths.where((x) => x == 'update'), hasLength(1)); await close(tester);
  });
}
