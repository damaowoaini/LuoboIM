import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/meeting/booking/data/booking_contract.dart';
import 'package:imapp/features/meeting/booking/domain/booking_view.dart';

import 'booking_test_harness.dart';

void main() {
  test('booking view requires exact owner, full typed payload and no media identifier fallback', () {
    final f = BookingFixture(); final raw = f.record();
    final row = OwnerBookingView.fromRecord(BookingRecord.wire(raw, 'owner'), 'owner');
    expect(row.id.value, bookingOne); expect(row.title, '合成预约'); expect(row.editable, isTrue);
    expect(() => OwnerBookingView.fromRecord(BookingRecord.wire(raw, 'owner'), 'other'), throwsA(isA<BookingLocalFailure>()));
    expect(() => BookingID('legacy-room'), throwsA(isA<BookingLocalFailure>()));
    for (final patch in [<String, Object?>{'startTime': 123}, {'startTime': 1893456000000},
      {'meetingDuration': '3600'}, {'meetingName': 'bad\nname'}, {'inviteeUserIDList': ['owner']}, {'ex': 7}]) {
      expect(() => BookingPayload.wire({...f.payload(), ...patch}, 'owner'), throwsA(isA<BookingLocalFailure>()));
    }
  });
  test('editing preserves already recorded invitees and opaque Ex while cancellation has no old body', () {
    final f = BookingFixture(); final original = BookingPayload.wire({...f.payload(),
      'inviteeUserIDList': ['existing-peer'], 'ex': 'opaque-not-parsed'}, 'owner');
    final edited = original.edited('新主题', original.start, 1800, f.now);
    expect(edited.toWire()['inviteeUserIDList'], ['existing-peer']); expect(edited.ex, 'opaque-not-parsed');
    final cancelled = OwnerBookingView.fromRecord(BookingRecord.wire(f.record(state: 'cancelled'), 'owner'), 'owner');
    expect(cancelled.payload, isNull); expect(cancelled.editable, isFalse);
  });
}
