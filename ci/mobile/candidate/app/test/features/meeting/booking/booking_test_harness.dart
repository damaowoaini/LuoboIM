import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:sqflite_common_ffi/sqflite_ffi.dart';
import 'package:imapp/features/meeting/booking/data/booking_contract.dart';
import 'package:imapp/features/meeting/booking/data/booking_pending_journal.dart';
import 'package:imapp/features/meeting/booking/data/meeting_booking_repository.dart';
import 'package:imapp/features/meeting/booking/presentation/booking_controller.dart';

const bookingOne = '20000000-0000-4000-8000-000000000001';
const intentOne = '10000000-0000-4000-8000-000000000001';

final class BookingFixture implements BookingTransport {
  late Directory directory;
  late MeetingBookingRepository repository;
  late BookingController controller;
  bool current = true, loseReply = false;
  bool _directoryCreated = false, _journalOpened = false, _controllerCreated = false, _closed = false;
  final invalidations = StreamController<int>.broadcast(sync: true);
  final paths = <String>[];
  final requests = <Map<String, Object?>>[];
  final receipts = <String, Object?>{};
  final rows = <String, Map<String, Object?>>{};
  Future<Object?> Function(String, Map<String, Object?>)? intercept;
  final now = DateTime.utc(2030, 1, 1);
  Map<String, Object?> payload({String name = '合成预约'}) => {
    'meetingName': name, 'startTime': now.add(const Duration(minutes: 10)).millisecondsSinceEpoch ~/ 1000,
    'meetingDuration': 3600, 'inviteeUserIDList': <String>[], 'ex': '',
  };
  Map<String, Object?> record({String id = bookingOne, int version = 1, String state = 'scheduled'}) => {
    'bookingID': id, 'hostUserID': 'owner', 'version': version, 'state': state,
    'mediaReady': false, 'payload': state == 'cancelled' ? null : payload(),
  };
  BookingIntent intent({String id = intentOne}) => BookingIntent(id: id,
    operation: BookingOperation.create, frozenBody: {'clientRequestID': id, ...payload()});
  Future<void> open({bool released = true}) async {
    sqfliteFfiInit(); directory = await Directory.systemTemp.createTemp('luobo_booking_ui_'); _directoryCreated = true;
    final scope = BookingJournalScope(accountKey: bookingDigest(jsonEncode(['issuer', 'tenant', 'owner'])),
      issuer: 'issuer', tenantId: 'tenant', owner: 'owner', checkCurrent: () {
        if (!current) throw const BookingLocalFailure(BookingLocalFailureKind.staleSession);
      });
    final journal = await BookingPendingJournal.open(databaseFactoryFfi, '${directory.path}/journal.sqlite', scope);
    repository = MeetingBookingRepository(transport: this, journal: journal);
    _journalOpened = true;
    controller = BookingController(repository, released: released, now: () => now,
      invalidations: invalidations.stream);
    _controllerCreated = true;
    await controller.start();
  }
  @override
  Future<Object?> post(String path, Map<String, Object?> body) async {
    final action = path.split('/').last; paths.add(action);
    requests.add(bookingMap(jsonDecode(jsonEncode(body))));
    return intercept == null ? respond(action, body) : intercept!(action, body);
  }
  Object? respond(String action, Map<String, Object?> body) {
    if (action == 'list') return {'bookings': rows.values.toList(), 'hasMore': false};
    if (action == 'detail') return rows[body['bookingID']] ?? record();
    final id = body['clientRequestID']! as String;
    if (receipts.containsKey(id)) return receipts[id];
    if (action == 'receipt') return {'clientRequestID': id, 'found': false,
      'applied': false, 'definitiveNotApplied': false, 'replaySafe': true};
    if (action == 'resolve') {
      return receipts[id] = {'clientRequestID': id, 'found': true, 'applied': false,
        'definitiveNotApplied': true, 'replaySafe': true, 'action': 'resolve'};
    }
    final target = body['bookingID'] as String? ?? bookingOne;
    final version = (body['expectedVersion'] as int? ?? 0) + 1;
    final saved = {...record(id: target, version: version, state: action == 'cancel' ? 'cancelled' : 'scheduled'),
      'payload': action == 'cancel' ? null : {for (final key in payload().keys) key: body[key]}};
    rows[target] = saved;
    final result = receipts[id] = {'clientRequestID': id, 'found': true, 'applied': true,
      'definitiveNotApplied': false, 'replaySafe': true, 'action': action, 'appliedVersion': version, 'booking': saved};
    if (loseReply) { loseReply = false; throw StateError('synthetic lost reply after fixture commit'); }
    return result;
  }
  Future<void> close() async {
    if (_closed) return;
    _closed = true; current = false;
    if (_controllerCreated) controller.dispose();
    await invalidations.close();
    if (_journalOpened) await repository.journal.close();
    if (_directoryCreated) await directory.delete(recursive: true);
  }
}
