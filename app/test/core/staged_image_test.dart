import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/storage/staged_image.dart';
import 'package:imapp/core/tenant/session_access.dart';

import 'im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ImageStager stager;
  setUp(() async {
    harness = SdkHarness();
    await harness.start();
    stager = ImageStager(harness.gateway, SessionAccess(harness.session));
  });
  tearDown(() async => harness.dispose());

  test('staged image uses private owner directory and cleanup preserves picker source', () async {
    final original = File('${harness.directory.path}/original.png');
    await original.writeAsBytes(
      base64Decode(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jF9kAAAAASUVORK5CYII=',
      ),
    );
    final staged = await stager.prepare(original.path);
    expect(staged.width, 1);
    expect(staged.height, 1);
    expect(staged.contentType, 'image/png');
    expect(staged.file.path, isNot(original.path));
    expect(await staged.file.readAsBytes(), await original.readAsBytes());
    await staged.dispose();
    expect(await staged.file.exists(), isFalse);
    expect(await original.exists(), isTrue);
  });

  test(
    'unsupported or oversized image does not leave a staged private copy',
    () async {
      final unsupported = File('${harness.directory.path}/report.txt');
      await unsupported.writeAsString('not an image');
      final large = File('${harness.directory.path}/large.png');
      final handle = await large.open(mode: FileMode.write);
      await handle.truncate(20 * 1024 * 1024 + 1);
      await handle.close();
      for (final source in [unsupported, large]) {
        await expectLater(
          stager.prepare(source.path),
          throwsA(
            isA<AppFailure>().having(
              (e) => e.kind,
              'kind',
              FailureKind.invalidInput,
            ),
          ),
        );
        expect(await source.exists(), isTrue);
      }
      final directory = await harness.gateway.mediaDirectory(
        harness.session.state.generation,
      );
      expect(await directory.list().toList(), isEmpty);
    },
  );

  test('stale session cannot copy a new private image', () async {
    final source = File('${harness.directory.path}/original.png');
    await source.writeAsString('unread');
    await harness.session.signOut(revoke: false);
    await expectLater(stager.prepare(source.path), throwsA(isA<AppFailure>()));
    expect(await source.readAsString(), 'unread');
  });
}
