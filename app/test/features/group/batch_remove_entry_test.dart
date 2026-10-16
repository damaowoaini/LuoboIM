import 'package:flutter_test/flutter_test.dart';

import 'batch_remove_fixture.dart';

void main() {
  for (final row in [
    (
      name: 'owner sees explicit batch removal entry',
      role: 100,
      status: 0,
      shown: true,
    ),
    (
      name: 'muted admin retains batch management entry',
      role: 60,
      status: 3,
      shown: true,
    ),
    (
      name: 'ordinary member has no batch management entry',
      role: 20,
      status: 0,
      shown: false,
    ),
    (
      name: 'dismissed group exposes no batch management entry',
      role: 100,
      status: 2,
      shown: false,
    ),
  ]) {
    testWidgets(row.name, (tester) async {
      final f = BatchFixture();
      f.roles['alice'] = row.role;
      f.status = row.status;
      try {
        await f.mount(tester);
        expect(find.text('同名成员'), findsNWidgets(2));
        expect(f.entry, row.shown ? findsOneWidget : findsNothing);
        expect(f.kickCalls, isEmpty);
        expect(tester.takeException(), isNull);
      } finally {
        await f.close(tester);
      }
    });
  }
}
