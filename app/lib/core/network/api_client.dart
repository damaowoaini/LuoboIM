import 'dart:convert';
import 'dart:math';

import 'package:dio/dio.dart';

import '../config/app_config.dart';
import '../error/app_failure.dart';

/// An instance belongs to one endpoint and one session generation. Never
/// retarget an existing authenticated Dio client to another tenant.
final class ApiClient {
  ApiClient({
    required Uri base,
    required this._dio,
    this._token,
    this._isCurrent,
    this._onExpired,
  }) : _base = validateHttps(base.toString());

  final Uri _base;
  Uri get base => _base;
  final Dio _dio;
  final String? _token;
  final bool Function()? _isCurrent;
  final void Function()? _onExpired;
  final _pending = <CancelToken>{};
  bool _closed = false;
  static final _random = Random.secure();

  Future<Object?> post(String path, Map<String, Object?> data) async {
    _checkCurrent();
    if (path.startsWith('/') || path.contains('://') || path.contains('..')) {
      throw const AppFailure(FailureKind.configuration);
    }
    final uri = _base.resolve(path);
    if (uri.origin != _base.origin) {
      throw const AppFailure(FailureKind.configuration);
    }
    final cancel = CancelToken();
    _pending.add(cancel);
    try {
      final response = await _dio.postUri<Object?>(
        uri,
        data: data,
        cancelToken: cancel,
        options: Options(
          headers: {
            'operationID':
                '${DateTime.now().microsecondsSinceEpoch}-${_random.nextInt(1 << 32)}',
            if (_token != null) 'token': _token,
          },
          contentType: Headers.jsonContentType,
          responseType: ResponseType.json,
          sendTimeout: const Duration(seconds: 15),
          receiveTimeout: const Duration(seconds: 20),
          followRedirects: false,
          maxRedirects: 0,
        ),
      );
      _checkCurrent();
      Object? raw = response.data;
      if (raw is String) {
        try {
          raw = jsonDecode(raw);
        } catch (_) {
          throw const AppFailure(FailureKind.invalidResponse);
        }
      }
      if (raw is! Map || raw['errCode'] is! int) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      final code = raw['errCode'] as int;
      if (code != 0) {
        final failure = AppFailure.fromCode(code);
        if (failure.kind == FailureKind.sessionExpired && _token != null) {
          _onExpired?.call();
        }
        throw failure;
      }
      return raw['data'];
    } on DioException catch (error) {
      _checkCurrent();
      throw switch (error.type) {
        DioExceptionType.cancel => const AppFailure(FailureKind.cancelled),
        DioExceptionType.connectionTimeout ||
        DioExceptionType.sendTimeout ||
        DioExceptionType.receiveTimeout => const AppFailure(
          FailureKind.timeout,
        ),
        _ => const AppFailure(FailureKind.network),
      };
    } finally {
      _pending.remove(cancel);
    }
  }

  void _checkCurrent() {
    if (_closed || !(_isCurrent?.call() ?? true)) {
      throw const AppFailure(FailureKind.cancelled);
    }
  }

  void close() {
    _closed = true;
    for (final token in _pending.toList()) {
      token.cancel();
    }
    _pending.clear();
  }
}
