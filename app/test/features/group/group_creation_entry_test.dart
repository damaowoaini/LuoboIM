import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/contacts/presentation/select_contacts_page.dart';

import 'group_creation_entry_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('inbox exposes a real accessible group creation entry and reaches selection', (tester) async {
    final f = GroupEntryFixture();
    try {
      await f.mount(tester, CreationSurface.inbox);
      expect(find.byTooltip('创建群聊'), findsOneWidget);
      expect(tester.getSize(find.byTooltip('创建群聊')).shortestSide, greaterThanOrEqualTo(44));
      await f.open(tester);
      expect(find.byType(SelectContactsPage), findsOneWidget);
      expect(f.verificationRequests, hasLength(1));
      expect(f.verificationRequests.single.data, {'userID': 'alice', 'statusOnly': true});
      expect(f.creation.creates, isEmpty);
    } finally { await f.close(tester); }
  });

  testWidgets('single chat settings exposes its real expansion entry with the current peer', (tester) async {
    final f = GroupEntryFixture();
    try {
      await f.mount(tester, CreationSurface.chat);
      await f.settings(tester);
      expect(find.widgetWithText(ListTile, '创建群聊'), findsOneWidget);
      await tester.tap(f.entry);
      await f.until(tester, () => find.byType(SelectContactsPage).evaluate().isNotEmpty);
      expect(find.byKey(const ValueKey('fixed-group-member-bob')), findsOneWidget);
      expect(find.text('ID：alice'), findsOneWidget);
      expect(f.verificationRequests, isEmpty);
      expect(f.creation.creates, isEmpty);
    } finally { await f.close(tester); }
  });
}
