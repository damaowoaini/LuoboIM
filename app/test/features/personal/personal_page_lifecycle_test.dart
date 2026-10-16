import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/personal/data/personal_repository.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';
import 'package:imapp/features/personal/personal_provider.dart';
import 'package:imapp/features/personal/presentation/personal_page.dart';
import 'package:imapp/providers.dart';
import 'package:sqflite/sqflite.dart';

import '../../core/privacy_bootstrap_test.dart'
    show ConsentMemory, StoreProbe, ImProbe;

class _DelayedDatabase implements Database {
  bool closed = false;
  @override
  Future<List<Map<String, Object?>>> query(
    String table, {
    bool? distinct,
    List<String>? columns,
    String? where,
    List<Object?>? whereArgs,
    String? groupBy,
    String? having,
    String? orderBy,
    int? limit,
    int? offset,
  }) async => [
    {
      'kind': 'note',
      'id': 'fixture',
      'payload': jsonEncode({
        'id': 'fixture',
        'title': '慢存储中的笔记',
        'content': '正文',
        'createdAt': '2026-10-15T00:00:00Z',
      }),
    },
  ];
  @override
  Future<void> close() async {
    closed = true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  testWidgets(
    'an open personal page retains its repository while native storage is slow',
    (tester) async {
      final session = SessionCoordinator(store: StoreProbe(), im: ImProbe());
      final credentials = SessionCredentials(
        userId: 'fixture',
        tenantId: 'fixture',
        tenantName: 'fixture',
        domains: ['tenant.test'],
        chatToken: 'fixture-chat',
        imToken: 'fixture-im',
      );
      await session.signIn((_) async => credentials);
      final runtime = AppRuntime(
        config: const AppConfig(),
        consent: ConsentMemory(),
        routes: RouteConfigLoader(config: const AppConfig()),
        session: session,
        auth: (_, _) async => throw StateError('unused'),
      )..phase = AppPhase.online;
      final opening = Completer<Database>();
      final database = _DelayedDatabase();
      var created = 0;
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            appRuntimeProvider.overrideWith((ref) => runtime),
            personalRepositoryProvider.overrideWith((ref) {
              created++;
              final repository = PersonalRepository(
                accountKey: credentials.accountKey,
                isCurrent: () => true,
                open: (_) => opening.future,
              );
              ref.onDispose(() => unawaited(repository.close()));
              return repository;
            }),
          ],
          child: const MaterialApp(home: PersonalPage(kind: PersonalKind.note)),
        ),
      );
      await tester.pump();
      await tester.pump(const Duration(seconds: 1));
      opening.complete(database);
      await tester.pumpAndSettle();
      expect(find.text('慢存储中的笔记'), findsOneWidget);
      expect(database.closed, isFalse);
      expect(created, 1);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pump();
      expect(database.closed, isTrue);
      await session.dispose();
    },
  );
}
