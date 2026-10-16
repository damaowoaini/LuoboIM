import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';

enum BookingOperation { create, update, cancel }

enum BookingLocalFailureKind {
  invalid,
  corrupt,
  storage,
  staleSession,
  busy,
  staleResponse,
  conflict,
}

/// No raw server/SQLite exception, meeting content, identity or token in errors.
final class BookingLocalFailure implements Exception {
  const BookingLocalFailure(this.kind);
  final BookingLocalFailureKind kind;
  @override
  String toString() => 'BookingLocalFailure(${kind.name})';
}

final class BookingUnconfirmed implements Exception {
  const BookingUnconfirmed(this.clientRequestID);
  final String clientRequestID;
  @override
  String toString() => 'BookingUnconfirmed(<redacted>)';
}

String bookingUUID() {
  final random = Random.secure();
  final bytes = List<int>.generate(16, (_) => random.nextInt(256));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  final hex = bytes.map((n) => n.toRadixString(16).padLeft(2, '0')).join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
}

bool isBookingUUID(Object? value) =>
    value is String &&
    RegExp(
      r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
    ).hasMatch(value);

Map<String, Object?> bookingMap(Object? value) {
  if (value is! Map<Object?, Object?> ||
      value.keys.any((key) => key is! String)) {
    throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
  }
  return Map<String, Object?>.from(value);
}

Object? _canonical(Object? value) {
  if (value == null || value is String || value is bool || value is int) {
    return value;
  }
  if (value is List<Object?>) {
    return value.map(_canonical).toList(growable: false);
  }
  final map = bookingMap(value);
  return {
    for (final key in (map.keys.toList()..sort())) key: _canonical(map[key]),
  };
}

String bookingCanonical(Object? value) => jsonEncode(_canonical(value));
String bookingDigest(String value) =>
    sha256.convert(utf8.encode(value)).toString();

final class BookingIntent {
  BookingIntent({
    required this.id,
    required this.operation,
    required Map<String, Object?> frozenBody,
  }) {
    if (!isBookingUUID(id) || frozenBody['clientRequestID'] != id) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final keys = operation == BookingOperation.cancel
        ? {'clientRequestID', 'bookingID', 'expectedVersion'}
        : {
            'clientRequestID',
            'meetingName',
            'startTime',
            'meetingDuration',
            'inviteeUserIDList',
            'ex',
            if (operation == BookingOperation.update) ...{
              'bookingID',
              'expectedVersion',
            },
          };
    if (frozenBody.keys.toSet().difference(keys).isNotEmpty ||
        !frozenBody.keys.toSet().containsAll(keys)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    if (operation != BookingOperation.create &&
        (!isBookingUUID(frozenBody['bookingID']) ||
            frozenBody['expectedVersion'] is! int ||
            (frozenBody['expectedVersion'] as int) < 1 ||
            (frozenBody['expectedVersion'] as int) > 1000000000)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    if (operation != BookingOperation.cancel) {
      final name = frozenBody['meetingName'];
      final duration = frozenBody['meetingDuration'];
      final invitees = frozenBody['inviteeUserIDList'];
      final ex = frozenBody['ex'];
      if (name is! String ||
          name.trim().isEmpty ||
          utf8.encode(name).length > 256 ||
          frozenBody['startTime'] is! int ||
          duration is! int ||
          duration < 60 ||
          duration > 604800 ||
          invitees is! List<Object?> ||
          invitees.length > 100 ||
          invitees.any(
            (id) => id is! String || id.isEmpty || utf8.encode(id).length > 512,
          ) ||
          invitees.toSet().length != invitees.length ||
          ex is! String ||
          utf8.encode(ex).length > 8192) {
        throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
      }
    }
    frozenJson = bookingCanonical(frozenBody);
    if (utf8.encode(frozenJson).length > 32768) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    digest = bookingDigest(
      bookingCanonical({
        'operation': operation.name,
        'body': jsonDecode(frozenJson),
      }),
    );
  }

  final String id;
  final BookingOperation operation;
  late final String frozenJson;
  late final String digest;
  Map<String, Object?> get payload => bookingMap(jsonDecode(frozenJson));
  String? get target => operation == BookingOperation.create
      ? null
      : payload['bookingID'] as String;
  int? get expectedVersion => operation == BookingOperation.create
      ? null
      : payload['expectedVersion'] as int;
  @override
  String toString() => 'BookingIntent(<redacted>)';
}

final class BookingRecord {
  const BookingRecord(
    this.id,
    this.host,
    this.state,
    this.version,
    this.payload, {
    this.detailsAvailable = true,
  });
  final String id;
  final String host;
  final String state;
  final int version;
  final Map<String, Object?>? payload;

  /// Recovered local receipts deliberately contain no private booking body.
  final bool detailsAvailable;

  static BookingRecord wire(Object? value, String owner) {
    final map = bookingMap(value);
    if (!isBookingUUID(map['bookingID']) ||
        map['hostUserID'] != owner ||
        map['version'] is! int ||
        (map['version'] as int) < 1 ||
        (map['version'] as int) > 1000000000 ||
        map['mediaReady'] != false ||
        !{'scheduled', 'cancelled'}.contains(map['state']) ||
        {
          'token',
          'joinToken',
          'roomID',
          'liveURL',
          'certificate',
        }.any(map.containsKey)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final payload = map['payload'];
    if ((map['state'] == 'cancelled' && payload != null) ||
        (map['state'] == 'scheduled' && payload is! Map<Object?, Object?>)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    return BookingRecord(
      map['bookingID'] as String,
      owner,
      map['state'] as String,
      map['version'] as int,
      payload == null ? null : Map.unmodifiable(bookingMap(payload)),
    );
  }
}

final class BookingReceipt {
  const BookingReceipt({
    required this.id,
    required this.found,
    required this.applied,
    required this.definitiveNotApplied,
    required this.action,
    required this.appliedVersion,
    required this.record,
  });
  final String id;
  final bool found;
  final bool applied;
  final bool definitiveNotApplied;
  final String? action;
  final int? appliedVersion;
  final BookingRecord? record;

  static BookingReceipt wire(String id, String owner, Object? value) {
    final map = bookingMap(value);
    if (map['clientRequestID'] != id ||
        map['replaySafe'] != true ||
        map['found'] is! bool ||
        map['applied'] is! bool ||
        map['definitiveNotApplied'] is! bool ||
        {
          'token',
          'joinToken',
          'roomID',
          'liveURL',
          'certificate',
        }.any(map.containsKey)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final found = map['found'] as bool,
        applied = map['applied'] as bool,
        rejected = map['definitiveNotApplied'] as bool;
    final action = map['action'], version = map['appliedVersion'];
    if ((action != null && action is! String) ||
        (version != null && version is! int) ||
        (!found &&
            (applied ||
                rejected ||
                map['booking'] != null ||
                action != null ||
                (version != null && version != 0))) ||
        (applied &&
            (!found ||
                rejected ||
                !{'create', 'update', 'cancel'}.contains(action) ||
                version is! int ||
                version < 1)) ||
        (rejected &&
            (!found ||
                applied ||
                action != 'resolve' ||
                map['booking'] != null ||
                (version != null && version != 0))) ||
        (found && !applied && !rejected)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final row = map['booking'] == null
        ? null
        : BookingRecord.wire(map['booking'], owner);
    if (applied && (row == null || (version as int) > row.version)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    return BookingReceipt(
      id: id,
      found: found,
      applied: applied,
      definitiveNotApplied: rejected,
      action: action as String?,
      appliedVersion: version as int?,
      record: row,
    );
  }

  void verifyIntent(BookingOperation operation, String? target) {
    if (applied &&
        (action != operation.name ||
            (operation != BookingOperation.create && record?.id != target))) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
  }

  /// Deliberately whitelist only final proof metadata, never raw HTTP/payload.
  String minimalJson() => bookingCanonical({
    'clientRequestID': id,
    'found': found,
    'applied': applied,
    'definitiveNotApplied': definitiveNotApplied,
    'action': action,
    'appliedVersion': appliedVersion,
    'booking': record == null
        ? null
        : {
            'bookingID': record!.id,
            'state': record!.state,
            'version': record!.version,
          },
  });

  static BookingReceipt minimal(String id, String owner, String json) {
    final map = bookingMap(jsonDecode(json));
    if (map.keys.toSet().difference({
      'clientRequestID',
      'found',
      'applied',
      'definitiveNotApplied',
      'action',
      'appliedVersion',
      'booking',
    }).isNotEmpty) {
      throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
    }
    final row = map['booking'];
    if (row != null) {
      final tiny = bookingMap(row);
      if (tiny.keys.toSet().difference({
        'bookingID',
        'state',
        'version',
      }).isNotEmpty) {
        throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
      }
      map['booking'] = {
        ...tiny,
        'hostUserID': owner,
        'mediaReady': false,
        'payload': tiny['state'] == 'scheduled' ? <String, Object?>{} : null,
      };
    }
    final result = wire(id, owner, {...map, 'replaySafe': true});
    if (!result.found) {
      throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
    }
    return BookingReceipt(
      id: result.id,
      found: result.found,
      applied: result.applied,
      definitiveNotApplied: result.definitiveNotApplied,
      action: result.action,
      appliedVersion: result.appliedVersion,
      record: result.record == null
          ? null
          : BookingRecord(
              result.record!.id,
              owner,
              result.record!.state,
              result.record!.version,
              null,
              detailsAvailable: false,
            ),
    );
  }
}
