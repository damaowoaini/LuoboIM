import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_background_store.dart';

import 'chat_background_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('chat background absent is distinct from a durable copied image after the picker original disappears', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      expect((await f.preferences.read()).revision, 'absent');
      final saved = await f.seed();
      expect(saved.image!.path, isNot(f.original.path));
      expect(await f.original.exists(), isTrue);
      await f.original.delete();
      final reopened = ChatBackgroundStore(supportDirectory: () async => f.support);
      try {
        final value = await reopened.read(f.preferences.accountKey, f.preferences.conversationId, current: () => true);
        expect(value.revision, saved.revision);
        expect(await value.image!.exists(), isTrue);
      } finally { reopened.dispose(); }
    } finally { await f.closeUnit(); }
  });
  test('chat background exact CID namespace isolates different conversations including opaque path characters', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed();
      final other = ChatBackgroundPreferences(f.store, f.font.sdk.session, '../../foreign:conversation');
      expect((await other.read()).revision, 'absent');
      await other.save(ChatBackgroundSnapshot.empty, f.source(), sourceCurrent: () => true);
      expect((await f.preferences.read()).revision, saved.revision);
      expect(saved.image!.path, isNot(contains('single-alice-bob')));
    } finally { await f.closeUnit(); }
  });
  for (final scope in ['issuer', 'tenant']) {
    test('chat background same UID remains isolated after a real $scope owner replacement', () async {
      final f = BackgroundFixture(); await f.start();
      try {
        final saved = await f.seed(); final old = f.preferences;
        await f.font.changeOwner(issuer: scope == 'issuer' ? 'https://new-issuer.example.com/' : f.font.sdk.credentials.issuer,
          tenant: scope == 'tenant' ? 'other-tenant' : f.font.sdk.credentials.tenantId);
        expect(old.current, isFalse);
        final next = ChatBackgroundPreferences(f.store, f.font.sdk.session, old.conversationId);
        expect(next.accountKey, isNot(old.accountKey));
        expect((await next.read()).revision, 'absent');
        expect(await saved.image!.exists(), isTrue);
        await expectLater(old.save(saved, null, sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      } finally { await f.closeUnit(); }
    });
  }
  test('chat background acknowledged default removes only its exact predecessor and persists across reopen', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed();
      final sibling = File('${saved.image!.parent.path}/unrelated.txt'); await sibling.writeAsString('owned by another feature');
      final reset = await f.preferences.save(saved, null, sourceCurrent: () => true);
      expect(reset.revision, isNot('absent')); expect(reset.image, isNull);
      expect((await f.preferences.read()).image, isNull);
      expect(await saved.image!.exists(), isFalse);
      expect(await sibling.readAsString(), 'owned by another feature');
      expect(await f.original.exists(), isTrue);
    } finally { await f.closeUnit(); }
  });
  test('chat background atomic metadata failure preserves the old pointer image and picker source for explicit retry', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed(); f.failReplace = true;
      await expectLater(f.preferences.save(saved, f.source(), sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      expect((await f.preferences.read()).revision, saved.revision);
      expect(await saved.image!.exists(), isTrue); expect(await f.original.exists(), isTrue);
      f.failReplace = false;
      final retry = await f.preferences.save(saved, f.source(), sourceCurrent: () => true);
      expect(retry.revision, isNot(saved.revision));
    } finally { await f.closeUnit(); }
  });
  test('chat background two editors cannot overwrite an acknowledged newer revision with a stale draft', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final a = await f.preferences.read(), b = await f.preferences.read();
      final first = await f.preferences.save(a, f.source(), sourceCurrent: () => true);
      await expectLater(f.preferences.save(b, null, sourceCurrent: () => true),
        throwsA(isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.dataChanged)));
      expect((await f.preferences.read()).revision, first.revision);
      expect(f.replacements, 1);
    } finally { await f.closeUnit(); }
  });
  test('chat background invalid stored path is an error and never authorizes outside file deletion', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed();
      final outside = File('${f.support.path}/keep.png'); await f.original.copy(outside.path);
      final pointer = File('${saved.image!.parent.path}/background.json');
      final record = jsonDecode(await pointer.readAsString()) as Map<String, dynamic>;
      record['image'] = '../../keep.png'; await pointer.writeAsString(jsonEncode(record));
      await expectLater(f.preferences.read(), throwsA(isA<AppFailure>()));
      await expectLater(f.preferences.save(saved, null, sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      expect(await outside.exists(), isTrue); expect(await saved.image!.exists(), isTrue);
    } finally { await f.closeUnit(); }
  });
  test('chat background feature-directory symlink fails closed without touching its target', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final outside = await Directory('${f.support.path}/foreign').create();
      await Link('${f.support.path}/chat_backgrounds_v1').create(outside.path);
      await expectLater(f.preferences.read(), throwsA(isA<AppFailure>()));
      await expectLater(f.seed(), throwsA(isA<AppFailure>()));
      expect(await outside.list().toList(), isEmpty);
      expect(await f.original.exists(), isTrue);
    } finally { await f.closeUnit(); }
  });
  test('chat background FIFO rechecks a queued view before its physical file replacement', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      f.replaceReply = Completer<void>(); f.replaceEntered = Completer<void>();
      final first = f.seed(); await f.replaceEntered!.future;
      var current = true;
      final queued = f.preferences.save(ChatBackgroundSnapshot.empty, null, sourceCurrent: () => current);
      final observed = expectLater(queued, throwsA(isA<AppFailure>()));
      current = false; f.replaceReply!.complete(); await first; await observed;
      expect(f.replacements, 1);
      expect((await f.preferences.read()).image, isNotNull);
    } finally { await f.closeUnit(); }
  });
  test('chat background file digest corruption is not reported as an empty successful read', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed(); await saved.image!.writeAsBytes([1, 2, 3]);
      final broken = await f.preferences.read();
      expect(broken.unavailable, isTrue); expect(broken.revision, saved.revision);
      final reset = await f.preferences.save(broken, null, sourceCurrent: () => true);
      expect(reset.unavailable, isFalse); expect(reset.image, isNull);
      expect(await f.original.exists(), isTrue);
    } finally { await f.closeUnit(); }
  });
  test('chat background source symlink is rejected without deleting either link or original', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final link = Link('${f.support.path}/source.png'); await link.create(f.original.path);
      await expectLater(f.preferences.save(ChatBackgroundSnapshot.empty, f.source(File(link.path)),
        sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      expect(await link.exists(), isTrue); expect(await f.original.exists(), isTrue);
      expect(f.replacements, 0);
    } finally { await f.closeUnit(); }
  });
  test('chat background delayed support-directory read cannot adopt a newly logged in owner', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      f.supportReply = Completer<void>(); f.supportEntered = Completer<void>();
      final old = f.preferences;
      final read = old.read();
      final observed = expectLater(read, throwsA(isA<AppFailure>()));
      await f.supportEntered!.future; await f.font.changeOwner();
      f.supportReply!.complete(); await observed;
      expect(f.replacements, 0);
      final next = ChatBackgroundPreferences(f.store, f.font.sdk.session, old.conversationId);
      expect((await next.read()).revision, 'absent');
    } finally { await f.closeUnit(); }
  });
  test('chat background already dispatched pointer ACK remains bound to the old account after replacement', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      f.replaceReply = Completer<void>(); f.replaceEntered = Completer<void>();
      final old = f.preferences;
      final write = old.save(ChatBackgroundSnapshot.empty, f.source(), sourceCurrent: () => true);
      final observed = expectLater(write, throwsA(isA<AppFailure>()));
      await f.replaceEntered!.future;
      await f.font.changeOwner(); f.replaceReply!.complete(); await observed;
      final next = ChatBackgroundPreferences(f.store, f.font.sdk.session, old.conversationId);
      expect((await next.read()).revision, 'absent');
      final oldRead = await f.store.read(old.accountKey, old.conversationId, current: () => true);
      expect(await oldRead.image!.exists(), isTrue); expect(f.replacements, 1);
    } finally { await f.closeUnit(); }
  });
}
