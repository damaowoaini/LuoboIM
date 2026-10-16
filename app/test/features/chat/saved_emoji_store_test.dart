import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/chat/data/saved_emoji_store.dart';

import 'saved_emoji_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SavedEmojiFixture f;
  setUp(() async {
    f = SavedEmojiFixture();
    await f.start();
  });
  tearDown(() => f.closeUnit());
  test('saved emoji persisted final receipt reopens and deduplicates without a picker path or credential', () async {
    final first = await f.seed();
    final second = await f.store.add(
      f.access,
      first,
      f.uploadEntry(),
      current: () => true,
    );
    final reopened = SavedEmojiStore(readString: (key) async => f.values[key]);
    try {
      final rows = await reopened.read(f.access, current: () => true);
      expect(rows.entries.single.url, f.uploadEntry().url);
      expect(second.entries.length, 1);
      expect(f.values.values.single, isNot(contains(f.original.path)));
      expect(
        f.values.values.single,
        isNot(contains(f.access.credentials.chatToken)),
      );
      expect(f.values.values.single, contains('upload_ack'));
    } finally {
      reopened.dispose();
    }
  });
  test('saved emoji same user remains isolated across real issuer and tenant logins', () async {
    await f.seed();
    final old = f.access;
    await f.font.changeOwner();
    final next = SessionAccess(f.sdk.session);
    final empty = await f.store.read(next, current: () => true);
    expect(empty.entries, isEmpty);
    expect(next.credentials.accountKey, isNot(old.credentials.accountKey));
    await expectLater(
      f.store.read(old, current: () => true),
      throwsA(isA<AppFailure>()),
    );
    expect(f.values.length, 1);
  });
  test('saved emoji stale editor revision cannot overwrite a newer acknowledged list', () async {
    final old = await f.store.read(f.access, current: () => true);
    await f.store.add(
      f.access,
      old,
      f.uploadEntry('first'),
      current: () => true,
    );
    await expectLater(
      f.store.add(f.access, old, f.uploadEntry('stale'), current: () => true),
      throwsA(isA<AppFailure>()),
    );
    expect(
      (await f.store.read(
        f.access,
        current: () => true,
      )).entries.single.url.path,
      '/first.png',
    );
  });
  test('saved emoji definite preference failure preserves the old list and permits explicit retry', () async {
    final first = await f.seed();
    f.failWrite = true;
    await expectLater(
      f.store.remove(f.access, first, {
        first.entries.single.id,
      }, current: () => true),
      throwsA(isA<AppFailure>()),
    );
    expect(
      (await f.store.read(f.access, current: () => true)).entries.length,
      1,
    );
    f.failWrite = false;
    expect(
      (await f.store.remove(f.access, first, {
        first.entries.single.id,
      }, current: () => true)).entries,
      isEmpty,
    );
  });
  test('saved emoji lost local ACK is read back exactly and never blindly replayed', () async {
    f.failWrite = f.applyThenFail = true;
    final saved = await f.seed();
    expect(saved.entries.length, 1);
    expect(f.writes, 1);
    expect(
      (await f.store.read(f.access, current: () => true)).revision,
      saved.revision,
    );
  });
  test('saved emoji pending upload stores only opaque nonce and cannot start another upload', () async {
    final empty = await f.store.read(f.access, current: () => true);
    final pending = await f.store.beginUpload(
      f.access,
      empty,
      current: () => true,
    );
    expect(pending.pendingUpload, matches(RegExp(r'^[a-f0-9]{32}$')));
    expect(f.values.values.single, isNot(contains('https://')));
    await expectLater(
      f.store.beginUpload(f.access, pending, current: () => true),
      throwsA(isA<AppFailure>()),
    );
    final saved = await f.store.finishUpload(
      f.access,
      pending,
      pending.pendingUpload!,
      f.uploadEntry(),
      current: () => true,
    );
    expect(saved.pendingUpload, isNull);
    expect(saved.entries.length, 1);
  });
  test('saved emoji pending receipt requires the same nonce and explicit ending does not send', () async {
    final pending = await f.store.beginUpload(
      f.access,
      await f.store.read(f.access, current: () => true),
      current: () => true,
    );
    await expectLater(
      f.store.finishUpload(
        f.access,
        pending,
        'wrong',
        f.uploadEntry(),
        current: () => true,
      ),
      throwsA(isA<AppFailure>()),
    );
    final ended = await f.store.endPending(
      f.access,
      pending,
      current: () => true,
    );
    expect(ended.entries, isEmpty);
    expect(ended.pendingUpload, isNull);
    expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
  });
  test('saved emoji damaged metadata is an explicit error and never an empty successful list', () async {
    f.values[SavedEmojiStore.keyFor(f.access.credentials.accountKey)] =
        '{broken';
    await expectLater(
      f.store.read(f.access, current: () => true),
      throwsA(isA<AppFailure>()),
    );
    expect(f.values.values.single, '{broken');
  });
  test('saved emoji deleting list entries never removes picker originals or other feature files', () async {
    final foreign = File('${f.sdk.directory.path}/unrelated.png');
    await foreign.writeAsBytes(f.png);
    final old = await f.seed();
    await f.store.remove(f.access, old, {
      old.entries.single.id,
    }, current: () => true);
    expect(await f.original.exists(), isTrue);
    expect(await foreign.exists(), isTrue);
    expect(f.uploads, isEmpty);
  });
  test('saved emoji queued write rechecks its original owner and closed store rejects new work', () async {
    final old = await f.store.read(f.access, current: () => true);
    final barrier = f.defer(), entered = Completer<void>();
    f.beforeWrite = () async {
      if (!entered.isCompleted) entered.complete();
      await barrier.future;
    };
    final first = f.store.add(
      f.access,
      old,
      f.uploadEntry(),
      current: () => true,
    );
    final firstResult = expectLater(first, throwsA(isA<AppFailure>()));
    await entered.future;
    final queued = f.store.add(
      f.access,
      old,
      f.uploadEntry('queued'),
      current: () => true,
    );
    final queuedResult = expectLater(queued, throwsA(isA<AppFailure>()));
    await f.font.changeOwner();
    barrier.complete();
    await firstResult;
    await queuedResult;
    expect(f.writes, 1);
    f.store.dispose();
    await expectLater(
      f.store.read(SessionAccess(f.sdk.session), current: () => true),
      throwsA(isA<AppFailure>()),
    );
  });
}
