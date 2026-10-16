import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/access/native_access.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const native = MethodChannelNativeAccess();
  tearDown(
    () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(MethodChannelNativeAccess.channel, null),
  );
  test('biometric response must match the exact request ID, never a nonempty reset or success string', () async {
    for (final response in <Object?>[null, true, 'Data has been reset', 12]) {
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(
            MethodChannelNativeAccess.channel,
            (call) async => call.method == 'cancel' ? null : response,
          );
      await expectLater(
        native.biometric('a' * 64, 11),
        throwsA(isA<AccessFailure>()),
      );
    }
  });
  test('only native capabilities true enables biometric availability; no truthy or absent defaults', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(
          MethodChannelNativeAccess.channel,
          (_) async => false,
        );
    expect(await native.biometricAvailable(), false);
  });
  test('cancelled and system lockout have fixed safe messages without raw native details', () async {
    for (final code in ['cancelled', 'locked_out', 'unavailable']) {
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(MethodChannelNativeAccess.channel, (
            call,
          ) async {
            if (call.method == 'cancel') return null;
            throw PlatformException(
              code: code,
              message: 'synthetic-sensitive-native-detail',
            );
          });
      try {
        await native.biometric('a' * 64, 1);
        fail('must reject');
      } on AccessFailure catch (error) {
        expect(error.message, isNot(contains('synthetic-sensitive')));
        expect(error.kind, switch (code) {
          'cancelled' => AccessFailureKind.cancelled,
          'locked_out' => AccessFailureKind.lockedOut,
          _ => AccessFailureKind.unavailable,
        });
      }
    }
  });
  test('PIN native bridge declares exact algorithm inputs and returns only its derived bytes', () async {
    final salt = base64Encode(List.filled(32, 1)),
        derived = base64Encode(List.filled(32, 2));
    MethodCall? received;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(MethodChannelNativeAccess.channel, (
          call,
        ) async {
          received = call;
          return derived;
        });
    expect(await native.derivePin('123456', salt, 210000), derived);
    expect(received!.method, 'derivePin');
    expect(received!.arguments, {
      'pin': '123456',
      'salt': salt,
      'iterations': 210000,
    });
  });
}
