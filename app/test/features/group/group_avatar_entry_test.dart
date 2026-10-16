import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/group/data/group_repository.dart';
import 'package:imapp/features/group/domain/group.dart';
import 'package:imapp/features/group/presentation/group_page.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  for (final row in [
    (
      name: 'owner has a visible existing-group avatar action',
      role: 100,
      status: 0,
      editable: true,
    ),
    (
      name: 'muted admin keeps management avatar action',
      role: 60,
      status: 3,
      editable: true,
    ),
    (
      name: 'ordinary member cannot edit group avatar',
      role: 20,
      status: 0,
      editable: false,
    ),
    (
      name: 'dismissed group has no avatar action',
      role: 100,
      status: 2,
      editable: false,
    ),
  ]) {
    testWidgets(row.name, (tester) async {
      final sdk = SdkHarness();
      await tester.runAsync(sdk.start);
      addTearDown(sdk.dispose);
      final repository = GroupRepository(
        sdk.gateway,
        SessionAccess(sdk.session),
      );
      final profile = GroupProfile(
        summary: GroupSummary(
          id: 'group-a',
          name: '设计讨论群',
          ownerId: 'alice',
          members: 3,
          sessionType: 3,
          status: row.status,
        ),
        joined: true,
        me: GroupMember(
          id: 'alice',
          name: '我',
          role: row.role,
          mutedUntil: 4102444800000,
        ),
        announcement: '',
        introduction: '',
        verification: 0,
        hideMemberProfiles: false,
        preventMemberFriends: false,
      );
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            groupRepositoryProvider.overrideWithValue(repository),
            groupProfileProvider('group-a').overrideWith((_) async => profile),
          ],
          child: MaterialApp(home: const GroupPage(groupId: 'group-a')),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('设计讨论群'), findsWidgets);
      expect(find.text('更换群头像'), row.editable ? findsOneWidget : findsNothing);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.runAsync(sdk.dispose);
    });
  }
}
