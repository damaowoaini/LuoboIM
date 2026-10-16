import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/error/app_failure.dart';

void main() {
  test(
    'fast failed mirror does not defeat a later successful source',
    () async {
      final slow = Completer<Object?>();
      final loader = RouteConfigLoader(
        config: AppConfig(
          routeSources: [
            Uri.parse('https://fast.test/config'),
            Uri.parse('https://slow.test/config'),
          ],
          fallbackRoutes: [Uri.parse('https://fallback.test/')],
        ),
        fetch: (uri, _) => uri.host == 'fast.test'
            ? Future.error(Exception('failed'))
            : slow.future,
      );
      final result = loader.load();
      await Future<void>.delayed(Duration.zero);
      slow.complete({
        'lines': [
          {'domian': 'route.test'},
        ],
      });
      expect((await result).single.host, 'route.test');
    },
  );

  test('all failures use the validated fallback', () async {
    final loader = RouteConfigLoader(
      config: AppConfig(
        routeSources: [Uri.parse('https://mirror.test/')],
        fallbackRoutes: [Uri.parse('https://fallback.test/')],
      ),
      fetch: (_, _) async => throw Exception('failed'),
    );
    expect((await loader.load()).single.host, 'fallback.test');
  });

  test('malformed and insecure config cannot produce a usable line', () {
    expect(
      RouteConfigLoader.parse({
        'routes': [
          {'url': 'http://insecure.test'},
          {'url': 'https://user:secret@evil.test'},
          {'url': 'https://valid.test'},
          {'url': 'https://valid.test/'},
        ],
      }).map((u) => u.host),
      ['valid.test'],
    );
    expect(RouteConfigLoader.parse('<html>error</html>'), isEmpty);
  });

  test(
    'missing bootstrap reports configuration error, not a demo service',
    () async {
      await expectLater(
        RouteConfigLoader(config: const AppConfig()).load(),
        throwsA(isA<AppFailure>()),
      );
    },
  );
}
