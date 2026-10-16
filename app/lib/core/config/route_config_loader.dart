import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';

import '../error/app_failure.dart';
import 'app_config.dart';

typedef RouteConfigFetch = Future<Object?> Function(
  Uri uri,
  CancelToken cancellation,
);

final class RouteConfigLoader {
  RouteConfigLoader({
    required this.config,
    RouteConfigFetch? fetch,
    this.timeout = const Duration(seconds: 5),
  }) : _fetch = fetch ?? _defaultFetch;
  final AppConfig config;
  final RouteConfigFetch _fetch;
  final Duration timeout;

  static Future<Object?> _defaultFetch(Uri uri, CancelToken token) async {
    final dio = Dio(
      BaseOptions(
        connectTimeout: const Duration(seconds: 5),
        receiveTimeout: const Duration(seconds: 5),
        followRedirects: false,
      ),
    );
    try {
      return (await dio.getUri<Object?>(uri, cancelToken: token)).data;
    } finally {
      dio.close(force: true);
    }
  }

  Future<List<Uri>> load() async {
    if (config.routeSources.isEmpty) return _fallback();
    final winner = Completer<List<Uri>>();
    final tokens = List.generate(
      config.routeSources.length,
      (_) => CancelToken(),
    );
    var remaining = tokens.length;
    for (var i = 0; i < tokens.length; i++) {
      final token = tokens[i];
      () async {
        try {
          final body = await _fetch(
            config.routeSources[i],
            token,
          ).timeout(timeout);
          final routes = parse(body);
          if (routes.isNotEmpty && !winner.isCompleted) {
            winner.complete(routes);
            for (final other in tokens) {
              if (other != token) other.cancel();
            }
          }
        } catch (_) {
          // A fast failure must not beat a slower valid configuration.
        } finally {
          if (!token.isCancelled) token.cancel();
          remaining--;
          if (remaining == 0 && !winner.isCompleted) {
            try {
              winner.complete(_fallback());
            } catch (error, stack) {
              winner.completeError(error, stack);
            }
          }
        }
      }();
    }
    return winner.future;
  }

  List<Uri> _fallback() {
    if (config.fallbackRoutes.isEmpty) {
      throw const AppFailure(FailureKind.configuration);
    }
    return List.unmodifiable(
      config.fallbackRoutes.map((u) => validateHttps(u.toString())),
    );
  }

  static List<Uri> parse(Object? body) {
    Object? decoded = body;
    if (body is String) {
      try {
        decoded = jsonDecode(body);
      } catch (_) {
        return const [];
      }
    }
    final entries = switch (decoded) {
      List values => values,
      Map values => values['lines'] ?? values['routes'],
      _ => null,
    };
    if (entries is! List) return const [];
    final found = <String, Uri>{};
    for (final entry in entries) {
      final raw = entry is Map
          ? entry['url'] ?? entry['domain'] ?? entry['domian']
          : entry;
      if (raw is! String || raw.trim().isEmpty) continue;
      try {
        final value = raw.trim();
        final uri = validateHttps(
          value.contains('://') ? value : 'https://$value',
        );
        if (uri.hasQuery) continue;
        final normalized = uri.replace(
          path: uri.path.endsWith('/') ? uri.path : '${uri.path}/',
        );
        found[normalized.toString()] = normalized;
      } on AppFailure {
        continue;
      }
    }
    return List.unmodifiable(found.values);
  }
}
