import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/mine/account_providers.dart';
import 'package:imapp/features/mine/data/account_repository.dart';
import 'package:imapp/features/mine/presentation/account_profile_page.dart';
import 'package:imapp/features/mine/presentation/account_settings_page.dart';

void main() {
  for (final entry in {
    'profile': const AccountProfilePage(),
    'settings': const AccountSettingsPage(),
  }.entries) {
    testWidgets(
      '${entry.key} fits small viewport at1.6 text scale and remains scrollable',
      (tester) async {
        tester.view.physicalSize = const Size(360, 640);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        await tester.pumpWidget(
          ProviderScope(
            overrides: [
              accountProfileProvider.overrideWith(
                (_) async => AccountProfile(
                  userId: List.filled(64, 'a').join(),
                  nickname: '这是一段较长的中文昵称用于检验换行',
                  email: 'synthetic-long-account-name@example.com',
                  birthday: DateTime(1950, 6, 15),
                  friendRequests: true,
                  sound: true,
                  globalReceive: 2,
                ),
              ),
            ],
            child: MaterialApp(
              builder: (context, child) => MediaQuery(
                data: MediaQuery.of(context)
                    .copyWith(textScaler: const TextScaler.linear(1.6)),
                child: child!,
              ),
              home: entry.value,
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        await tester.drag(find.byType(ListView), const Offset(0, -600));
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        expect(
          find.text(entry.key == 'profile' ? '绑定手机' : '黑名单'),
          findsOneWidget,
        );
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }
}
