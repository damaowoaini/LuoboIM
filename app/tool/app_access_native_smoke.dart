import 'package:flutter/material.dart';

import 'package:imapp/core/access/native_access.dart';

/// Independent future native KDF smoke. No credentials, SDK or biometrics.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  const native = MethodChannelNativeAccess();
  const vectors = <(String, String)>[
    (
      'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
      '5hlt3Um3FEudEJnQbjl+Ze8ljmOmNKTQSmiL4jKv+no=',
    ),
    (
      'AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=',
      'mwmeZZ61hOwqSonf1655hIrWDd8GX9gAe5ymkoXtEw8=',
    ),
  ];
  for (final vector in vectors) {
    final actual = await native.derivePin('123456', vector.$1, 210000);
    if (actual != vector.$2) throw StateError('Native PIN KDF vector mismatch');
  }
  debugPrint('LUOBO_APP_ACCESS_KDF_PASS');
  runApp(
    const MaterialApp(
      home: Scaffold(
        body: Center(child: Text('Local access KDF smoke passed')),
      ),
    ),
  );
}
