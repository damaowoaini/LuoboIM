import 'dart:convert';

/// A bounded display projection of standard OpenIM LocationElem (type109).
/// No message URL, map key, GPS lookup or coordinate conversion is retained.
final class ChatLocation {
  const ChatLocation._(
    this.latitude,
    this.longitude,
    this.name,
    this.address,
    this.coordinateSystem,
  );

  final double latitude, longitude;
  final String name, address;
  // Sender-declared metadata, not evidence of a verified provider/datum.
  final String? coordinateSystem;

  static ChatLocation? parse({
    double? latitude,
    double? longitude,
    String? description,
  }) {
    if (latitude == null ||
        longitude == null ||
        !latitude.isFinite ||
        !longitude.isFinite ||
        latitude < -90 ||
        latitude > 90 ||
        longitude < -180 ||
        longitude > 180) {
      return null;
    }
    final source = description?.trim() ?? '';
    if (source.length > 4096) {
      return null;
    }
    String name = '位置', address = '';
    String? datum;
    if (source.startsWith('{') || source.startsWith('[')) {
      // Bound nesting BEFORE jsonDecode, including unknown fields. Never
      // decode a recursively nested attacker-controlled description first.
      if (!_flatJson(source)) {
        return null;
      }
      try {
        final value = jsonDecode(source);
        if (value is! Map ||
            value.length > 16 ||
            value.values.any((v) => v is Map || v is List)) {
          return null;
        }
        final title = _text(value['name'], 128);
        final detail = _text(value['addr'], 512);
        if (title == null || detail == null) {
          return null;
        }
        if (title.isNotEmpty) {
          name = title;
        }
        address = detail;
        datum = switch (value['coordinateSystem']) {
          'wgs84' => 'WGS84',
          'gcj02' => 'GCJ-02',
          _ => null,
        };
        // In particular, ignore legacy url/latitude/longitude fields. Only
        // LocationElem's explicit numeric coordinates are authoritative here.
      } on FormatException {
        return null;
      }
    } else {
      final plain = _text(source, 512);
      if (plain == null) {
        return null;
      }
      address = plain;
    }
    return ChatLocation._(latitude, longitude, name, address, datum);
  }

  static String? _text(Object? value, int maximumRunes) {
    if (value == null) {
      return '';
    }
    if (value is! String || value.runes.length > maximumRunes) {
      return null;
    }
    // Text is not rich text or a resource locator. Suppress URL-shaped labels
    // as well as control/bidi characters instead of exposing an old map key.
    if (RegExp(r'https?://', caseSensitive: false).hasMatch(value)) {
      return '';
    }
    return value
        .replaceAll(RegExp(r'[\x00-\x1f\x7f\u202a-\u202e\u2066-\u2069]'), ' ')
        .trim();
  }

  static bool _flatJson(String value) {
    var depth = 0;
    var quoted = false, escaped = false;
    for (final code in value.codeUnits) {
      if (quoted) {
        if (escaped) {
          escaped = false;
        } else if (code == 92) {
          escaped = true;
        } else if (code == 34) {
          quoted = false;
        }
      } else if (code == 34) {
        quoted = true;
      } else if (code == 123 || code == 91) {
        if (++depth > 1) {
          return false;
        }
      } else if (code == 125 || code == 93) {
        if (--depth < 0) {
          return false;
        }
      }
    }
    return depth == 0 && !quoted;
  }

  @override
  bool operator ==(Object other) =>
      other is ChatLocation &&
      latitude == other.latitude &&
      longitude == other.longitude &&
      name == other.name &&
      address == other.address &&
      coordinateSystem == other.coordinateSystem;
  @override
  int get hashCode =>
      Object.hash(latitude, longitude, name, address, coordinateSystem);
  @override
  String toString() => 'ChatLocation';
}
