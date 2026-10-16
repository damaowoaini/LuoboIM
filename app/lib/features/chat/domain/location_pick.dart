import 'dart:convert';
import 'dart:math';

import '../../../core/error/app_failure.dart';

String newLocationNonce([int bytes = 32]) {
  final random = Random.secure();
  return List.generate(
    bytes,
    (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
  ).join();
}

bool _keys(Map value, Set<String> keys) =>
    value.length == keys.length && value.keys.every(keys.contains);

bool _coordinate(Object? value, double limit) =>
    value is num && value.isFinite && value >= -limit && value <= limit;

Map _object(String source, int maximumBytes) {
  if (source.length > maximumBytes ||
      utf8.encode(source).length > maximumBytes) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  // Bound nesting before decoding; these interfaces contain scalar fields only.
  var depth = 0;
  var quoted = false, escaped = false;
  for (final c in source.codeUnits) {
    if (quoted) {
      if (escaped) {
        escaped = false;
      } else if (c == 92) {
        escaped = true;
      } else if (c == 34) {
        quoted = false;
      }
    } else if (c == 34) {
      quoted = true;
    } else if (c == 123 || c == 91) {
      if (++depth > 1) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
    } else if (c == 125 || c == 93) {
      if (--depth < 0) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
    }
  }
  if (quoted || depth != 0) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  try {
    final value = jsonDecode(source);
    if (value is Map && value.values.every((v) => v is! Map && v is! List)) {
      return value;
    }
  } on FormatException {
    // Never retain or print an untrusted JS message.
  }
  throw const AppFailure(FailureKind.invalidResponse);
}

final class LocationPickerConfig {
  const LocationPickerConfig._(this.uri, this.longitude, this.latitude);
  final Uri uri;
  final double longitude, latitude;

  static LocationPickerConfig? parse(String url, String lng, String lat) {
    final uri = Uri.tryParse(url);
    final longitude = double.tryParse(lng), latitude = double.tryParse(lat);
    if (uri == null ||
        uri.scheme != 'https' ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.path != '/picker' ||
        uri.hasQuery ||
        uri.hasFragment ||
        !_coordinate(longitude, 180) ||
        !_coordinate(latitude, 90)) {
      return null;
    }
    return LocationPickerConfig._(uri, longitude!, latitude!);
  }

  static LocationPickerConfig? fromEnvironment() => parse(
    const String.fromEnvironment('LUOBO_LOCATION_PICKER_URL'),
    const String.fromEnvironment('LUOBO_LOCATION_INITIAL_LNG'),
    const String.fromEnvironment('LUOBO_LOCATION_INITIAL_LAT'),
  );

  bool allows(String url) {
    final next = Uri.tryParse(url);
    return next != null &&
        next.scheme == 'https' &&
        next.host == uri.host &&
        next.port == uri.port &&
        next.path == '/picker' &&
        next.userInfo.isEmpty &&
        !next.hasQuery &&
        !next.hasFragment;
  }
}

final class LocationPick {
  const LocationPick._(
    this.requestNonce,
    this.sequence,
    this.longitude,
    this.latitude,
    this.name,
  );
  final String requestNonce, name;
  final int sequence;
  final double longitude, latitude;

  static LocationPick parse(
    String source, {
    required String nonce,
    required int afterSequence,
  }) {
    final value = _object(source, 2048);
    final sequence = value['sequence'], name = value['name'];
    if (!_keys(value, {'requestNonce', 'sequence', 'lng', 'lat', 'name'}) ||
        value['requestNonce'] != nonce ||
        !RegExp(r'^[a-f0-9]{64}$').hasMatch(nonce) ||
        sequence is! int ||
        sequence <= afterSequence ||
        sequence > 9007199254740991 ||
        !_coordinate(value['lng'], 180) ||
        !_coordinate(value['lat'], 90) ||
        name is! String ||
        name.runes.length > 128 ||
        utf8.decode(utf8.encode(name)) != name ||
        RegExp(
          r'[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]|(?:https?:|www\.|javascript:|data:|file:)',
          caseSensitive: false,
        ).hasMatch(name)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return LocationPick._(
      nonce,
      sequence,
      (value['lng'] as num).toDouble(),
      (value['lat'] as num).toDouble(),
      name.trim().isEmpty ? '位置' : name.trim(),
    );
  }

  String get description => jsonEncode({
    'v': 1,
    'name': name,
    'addr': '',
    'coordinateSystem': 'gcj02',
  });

  @override
  String toString() => 'LocationPick';
}

final class LocationMapStatus {
  const LocationMapStatus._(this.ready, this.sequence, this.code);
  final bool ready;
  final int sequence;
  final String? code;

  static LocationMapStatus parse(String source, String nonce) {
    final value = _object(source, 512);
    final sequence = value['sequence'];
    final ready = value['state'] == 'ready';
    if (!_keys(value, {
          'requestNonce',
          'sequence',
          'state',
          if (!ready) 'code',
        }) ||
        value['requestNonce'] != nonce ||
        sequence is! int ||
        sequence < 0 ||
        sequence > 9007199254740991 ||
        (!ready &&
            (value['state'] != 'error' ||
                !const {
                  'config',
                  'map_load',
                  'conversion',
                  'stale_command',
                }.contains(value['code'])))) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return LocationMapStatus._(ready, sequence, value['code'] as String?);
  }
}

final class LocationFix {
  const LocationFix._(
    this.longitude,
    this.latitude,
    this.accuracyMeters,
    this.approximate,
  );
  final double longitude, latitude, accuracyMeters;
  final bool approximate;

  static LocationFix parse(Object? result, String id, DateTime now) {
    if (result is! Map ||
        !_keys(result, {
          'requestId',
          'longitude',
          'latitude',
          'accuracyMeters',
          'timestampMillis',
          'coordinateSystem',
          'approximate',
        }) ||
        result['requestId'] != id ||
        result['coordinateSystem'] != 'wgs84' ||
        !_coordinate(result['longitude'], 180) ||
        !_coordinate(result['latitude'], 90) ||
        result['accuracyMeters'] is! num ||
        !(result['accuracyMeters'] as num).isFinite ||
        (result['accuracyMeters'] as num) < 0 ||
        result['approximate'] is! bool ||
        result['timestampMillis'] is! int) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final age = now.millisecondsSinceEpoch - (result['timestampMillis'] as int);
    if (age < -2000 || age > 10000) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return LocationFix._(
      (result['longitude'] as num).toDouble(),
      (result['latitude'] as num).toDouble(),
      (result['accuracyMeters'] as num).toDouble(),
      result['approximate'] as bool,
    );
  }
}

enum LocationSendKind { sent, notDispatched, unconfirmed }

final class LocationSendResult {
  const LocationSendResult(this.kind, [this.messageId]);
  final LocationSendKind kind;
  final String? messageId;
}
