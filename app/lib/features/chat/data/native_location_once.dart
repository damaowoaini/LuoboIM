import 'dart:async';

import 'package:flutter/services.dart';

import '../../../core/error/app_failure.dart';
import '../domain/location_pick.dart';

abstract interface class NativeLocationOnce {
  Future<LocationFix> request(String id);
  Future<void> cancel(String id);
}

final class MethodChannelLocationOnce implements NativeLocationOnce {
  MethodChannelLocationOnce({DateTime Function()? now})
    : _now = now ?? DateTime.now;
  static const channel = MethodChannel('luobo/location_once');
  final DateTime Function() _now;
  String? _pending;

  @override
  Future<LocationFix> request(String id) async {
    if (_pending != null || !RegExp(r'^[a-f0-9]{32}$').hasMatch(id)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    _pending = id;
    try {
      final result = await channel
          .invokeMethod<Object?>('request', {
            'requestId': id,
            'timeoutMs': 15000,
          })
          .timeout(const Duration(seconds: 17));
      if (_pending != id) {
        throw const AppFailure(FailureKind.cancelled);
      }
      return LocationFix.parse(result, id, _now());
    } on TimeoutException {
      throw const AppFailure(FailureKind.timeout);
    } on PlatformException catch (error) {
      throw AppFailure(switch (error.code) {
        'cancelled' => FailureKind.cancelled,
        'denied' || 'permanently_denied' => FailureKind.permissionDenied,
        'timeout' => FailureKind.timeout,
        'service_off' || 'unavailable' => FailureKind.configuration,
        'invalid_request' => FailureKind.invalidInput,
        _ => FailureKind.unknown,
      });
    } on MissingPluginException {
      throw const AppFailure(FailureKind.configuration);
    } finally {
      if (_pending == id) {
        _pending = null;
      }
      await cancel(id);
    }
  }

  @override
  Future<void> cancel(String id) async {
    if (_pending == id) {
      _pending = null;
    }
    try {
      await channel
          .invokeMethod<Object?>('cancel', {'requestId': id})
          .timeout(const Duration(seconds: 2));
    } catch (_) {
      // Request timeout/retired source is still an error; this best-effort
      // cleanup neither creates a success nor replays a native request.
    }
  }
}
