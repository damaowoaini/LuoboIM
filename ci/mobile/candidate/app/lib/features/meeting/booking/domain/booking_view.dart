import 'dart:convert';

import '../data/booking_contract.dart';

final class BookingID {
  BookingID(this.value) {
    if (!isBookingUUID(value)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
  }
  final String value;
}

final class BookingPayload {
  BookingPayload({required this.title, required this.startSeconds,
    required this.durationSeconds, this.invitees = const [], this.ex = ''});
  final String title, ex;
  final int startSeconds, durationSeconds;
  final List<String> invitees;
  DateTime get start => DateTime.fromMillisecondsSinceEpoch(startSeconds * 1000, isUtc: true);
  DateTime get end => DateTime.fromMillisecondsSinceEpoch((startSeconds + durationSeconds) * 1000, isUtc: true);

  factory BookingPayload.wire(Map<String, Object?> raw, String owner) {
    const keys = {'meetingName', 'startTime', 'meetingDuration', 'inviteeUserIDList', 'ex'};
    if (raw.length != keys.length || !raw.keys.toSet().containsAll(keys)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final name = raw['meetingName'], start = raw['startTime'], duration = raw['meetingDuration'];
    final ids = raw['inviteeUserIDList'], ex = raw['ex'];
    if (name is! String || name.trim().isEmpty || utf8.encode(name).length > 256 ||
        RegExp(r'[\x00-\x1f\x7f]').hasMatch(name) || start is! int ||
        start < 946684800 || start > 4102444800 || duration is! int || duration < 60 ||
        duration > 604800 || start + duration > 4103049600 || ids is! List || ids.length > 100 ||
        ex is! String || utf8.encode(ex).length > 8192) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final checked = <String>[];
    for (final id in ids) {
      if (id is! String || id.isEmpty || id == owner || id.trim() != id ||
          utf8.encode(id).length > 64 || RegExp(r'[\x00-\x1f\x7f]').hasMatch(id) || checked.contains(id)) {
        throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
      }
      checked.add(id);
    }
    return BookingPayload(title: name, startSeconds: start, durationSeconds: duration,
      invitees: List.unmodifiable(checked), ex: ex);
  }

  Map<String, Object?> toWire() => {'meetingName': title, 'startTime': startSeconds,
    'meetingDuration': durationSeconds, 'inviteeUserIDList': invitees, 'ex': ex};

  BookingPayload edited(String name, DateTime start, int duration, DateTime now) {
    final next = BookingPayload.wire({'meetingName': name.trim(),
      'startTime': start.millisecondsSinceEpoch ~/ 1000, 'meetingDuration': duration,
      'inviteeUserIDList': invitees, 'ex': ex}, '');
    if ((next.start.isBefore(now.subtract(const Duration(minutes: 1))) && next.startSeconds != startSeconds) ||
        next.start.isAfter(now.add(const Duration(days: 365)))) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    return next;
  }
}

final class OwnerBookingView {
  OwnerBookingView._(this.id, this.version, this.state, this.payload);
  final BookingID id;
  final int version;
  final String state;
  final BookingPayload? payload;
  String get title => payload?.title ?? '已取消预约';
  bool get editable => state == 'scheduled' && payload != null && version < 1000000000;
  factory OwnerBookingView.fromRecord(BookingRecord record, String owner) {
    if (record.host != owner || !record.detailsAvailable || record.version < 1 ||
        record.version > 1000000000 || !{'scheduled', 'cancelled'}.contains(record.state)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final payload = record.payload;
    if ((record.state == 'scheduled' && payload == null) || (record.state == 'cancelled' && payload != null)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    return OwnerBookingView._(BookingID(record.id), record.version, record.state,
      payload == null ? null : BookingPayload.wire(payload, owner));
  }
}

String bookingLocalTime(DateTime value) {
  final d = value.toLocal();
  String two(int n) => '$n'.padLeft(2, '0');
  return '${d.year}-${two(d.month)}-${two(d.day)} ${two(d.hour)}:${two(d.minute)}';
}
