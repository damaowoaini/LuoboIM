import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/storage/staged_image.dart';
import 'package:imapp/features/chat/domain/saved_emoji.dart';

import 'saved_emoji_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SavedEmojiFixture f;
  setUp(() async {
    f = SavedEmojiFixture();
    await f.start();
  });
  tearDown(() => f.closeUnit());
  test('saved emoji uses actual SDK Face index minus one with exact URL dimensions and frozen recipient', () async {
    final access = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final outcome = await f.controller.sendSavedEmoji(
      f.uploadEntry(),
      access: access,
      checkSource: () {},
    );
    expect(outcome, SavedEmojiSendOutcome.sent);
    final face =
        f.sdk.calls
                .singleWhere((c) => c.method == 'createFaceMessage')
                .arguments
            as Map;
    expect(face['index'], -1);
    expect(jsonDecode(face['data'] as String), f.uploadEntry().faceData);
    final sent =
        f.sdk.calls.singleWhere((c) => c.method == 'sendMessage').arguments
            as Map;
    expect(sent['userID'], 'bob');
    expect(sent['groupID'], anyOf(isNull, ''));
  });
  test(
    'saved emoji rejects malformed actual Face response before native send',
    () async {
      final previous = f.sdk.overrideCall;
      f.sdk.overrideCall = (call) async {
        final value = await previous!(call);
        if (call.method == 'createFaceMessage') {
          final raw = jsonDecode(value as String) as Map;
          raw['faceElem']['index'] = 0;
          return jsonEncode(raw);
        }
        return value;
      };
      final cut = f.sdk.gateway.historyAccess(
        f.repository.generation,
        conversationId: f.repository.conversationId,
      );
      await expectLater(
        f.repository.prepareSavedEmoji(
          f.uploadEntry(),
          access: cut,
          checkSource: () {},
        ),
        throwsA(isA<AppFailure>()),
      );
      expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
    },
  );
  test('saved emoji source view invalid before preparation dispatches neither Face nor message', () async {
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final result = await f.controller.sendSavedEmoji(
      f.uploadEntry(),
      access: cut,
      checkSource: () => throw const AppFailure(FailureKind.cancelled),
    );
    expect(result, SavedEmojiSendOutcome.notDispatched);
    expect(f.faces, 0);
    expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
  });
  test('saved emoji private and timed message pictures never become reusable library references', () async {
    for (final raw in [
      f.image(id: 'private', private: true),
      f.image(
        id: 'timed',
        private: true,
        expiry: DateTime.now().millisecondsSinceEpoch + 60000,
      ),
    ]) {
      f.sdk.messages.add(raw);
      await f.controller.refresh();
      final cut = f.sdk.gateway.historyAccess(
        f.repository.generation,
        conversationId: f.repository.conversationId,
      );
      await expectLater(
        f.repository.savedEmojiFromMessage(
          raw['clientMsgID'] as String,
          access: cut,
          checkSource: () {},
        ),
        throwsA(isA<AppFailure>()),
      );
    }
    f.sdk.conversations.single['isMsgDestruct'] = true;
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    await expectLater(
      f.repository.savedEmojiFromMessage(
        'source-image',
        access: cut,
        checkSource: () {},
      ),
      throwsA(isA<AppFailure>()),
    );
    expect(f.faces, 0);
    expect(f.writes, 0);
  });
  test('saved emoji source reference is exact CID ID and URL rather than a reusable URL fallback', () async {
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final entry = await f.repository.savedEmojiFromMessage(
      'source-image',
      access: cut,
      checkSource: () {},
    );
    final proof = await f.repository.savedEmojiProof(
      entry,
      access: cut,
      checkSource: () {},
    );
    proof();
    expect(entry.source!.conversationId, f.repository.conversationId);
    f.sdk.messages.removeWhere((m) => m['clientMsgID'] == 'source-image');
    await expectLater(
      f.repository.savedEmojiProof(entry, access: cut, checkSource: () {}),
      throwsA(isA<AppFailure>()),
    );
    expect(f.faces, 0);
  });
  test('saved emoji ambiguous duplicated source response cannot authorize display or forwarding', () async {
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final entry = await f.repository.savedEmojiFromMessage(
      'source-image',
      access: cut,
      checkSource: () {},
    );
    f.sdk.messages.add(f.image());
    await expectLater(
      f.repository.savedEmojiProof(entry, access: cut, checkSource: () {}),
      throwsA(isA<AppFailure>()),
    );
    expect(f.faces, 0);
  });
  test('saved emoji a clear invalidates the captured proof and never renews its history lease', () async {
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final proof = await f.repository.savedEmojiProof(
      f.uploadEntry(),
      access: cut,
      checkSource: () {},
    );
    await f.repository.clearHistory();
    expect(proof, throwsA(isA<AppFailure>()));
    expect(f.faces, 0);
  });
  test('saved emoji owner replacement during exact source lookup prevents later Face creation', () async {
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final entry = await f.repository.savedEmojiFromMessage(
      'source-image',
      access: cut,
      checkSource: () {},
    );
    final reply = f.defer(), entered = Completer<void>();
    f.beforeFind = () async {
      if (!entered.isCompleted) entered.complete();
      await reply.future;
    };
    final pending = f.repository.prepareSavedEmoji(
      entry,
      access: cut,
      checkSource: () {},
    );
    final failed = expectLater(pending, throwsA(isA<AppFailure>()));
    await entered.future;
    // Revoke interactive access first; release the owned read so real SDK drain can end.
    final signout = f.sdk.session.signOut();
    reply.complete();
    await failed;
    await signout;
    expect(f.faces, 0);
  });
  test('saved emoji late view retirement after Face creation prevents final native send', () async {
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final reply = f.defer(), entered = Completer<void>();
    var current = true;
    f.beforeFace = () async {
      entered.complete();
      await reply.future;
    };
    final pending = f.controller.sendSavedEmoji(
      f.uploadEntry(),
      access: cut,
      checkSource: () {
        if (!current) {
          throw const AppFailure(FailureKind.cancelled);
        }
      },
    );
    await entered.future;
    current = false;
    reply.complete();
    expect(await pending, SavedEmojiSendOutcome.notDispatched);
    expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
    expect(f.controller.failure, isNull);
  });
  test('saved emoji unknown send never creates a second Face and explicit failed bubble retry keeps original ID', () async {
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    f.failSend = true;
    expect(
      await f.controller.sendSavedEmoji(
        f.uploadEntry(),
        access: cut,
        checkSource: () {},
      ),
      SavedEmojiSendOutcome.unconfirmed,
    );
    expect(
      await f.controller.sendSavedEmoji(
        f.uploadEntry(),
        access: cut,
        checkSource: () {},
      ),
      SavedEmojiSendOutcome.notDispatched,
    );
    expect(f.faces, 1);
    final id = f.controller.messages
        .firstWhere((m) => m.id.startsWith('face-'))
        .id;
    f.failSend = false;
    await f.controller.resend(id);
    final sent = f.sdk.calls
        .where((c) => c.method == 'sendMessage')
        .map((c) => (c.arguments as Map)['message']['clientMsgID'])
        .toList();
    expect(sent, [id, id]);
    expect(f.faces, 1);
  });
  test('saved emoji real signed uploader source gate blocks physical POST after form await', () async {
    final barrier = f.defer(), entered = Completer<void>();
    var current = true;
    f.beforeInitiate = () async {
      entered.complete();
      await barrier.future;
    };
    final uploader = f.uploader();
    try {
      final pending = uploader.upload(
        f.original,
        contentType: 'image/png',
        extension: '.png',
        group: 'saved-emoji',
        checkSource: () {
          if (!current) {
            throw const AppFailure(FailureKind.cancelled);
          }
        },
      );
      final failure = expectLater(pending, throwsA(isA<AppFailure>()));
      await entered.future;
      current = false;
      barrier.complete();
      await failure;
      expect(f.uploads, isEmpty);
      expect(
        f.metadata.requests.where((r) => r.path.endsWith('complete_form_data')),
        isEmpty,
      );
    } finally {
      uploader.close();
    }
  });
  test('saved emoji real signed uploader after physical POST reports failure without claiming no upload', () async {
    final barrier = f.defer(), entered = Completer<void>();
    var current = true;
    f.beforeUpload = () async {
      entered.complete();
      await barrier.future;
    };
    final uploader = f.uploader();
    try {
      final pending = uploader.upload(
        f.original,
        contentType: 'image/png',
        extension: '.png',
        group: 'saved-emoji',
        checkSource: () {
          if (!current) {
            throw const AppFailure(FailureKind.cancelled);
          }
        },
      );
      final failure = expectLater(pending, throwsA(isA<AppFailure>()));
      await entered.future;
      current = false;
      barrier.complete();
      await failure;
      expect(f.uploads.length, 1);
      expect(f.bodies.single, isNotEmpty);
      expect(
        f.metadata.requests.where((r) => r.path.endsWith('complete_form_data')),
        isEmpty,
      );
    } finally {
      uploader.close();
    }
  });

  test('saved emoji identical IDs in another CID never substitute a matching source URL', () async {
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final entry = await f.repository.savedEmojiFromMessage(
      'source-image',
      access: cut,
      checkSource: () {},
    );
    f.sdk.messages.removeWhere((m) => m['clientMsgID'] == 'source-image');
    f.sdk.messages.add({...f.image(), 'conversationID': 'foreign-cid'});
    await expectLater(
      f.repository.savedEmojiProof(entry, access: cut, checkSource: () {}),
      throwsA(isA<AppFailure>()),
    );
    expect(f.faces, 0);
    expect(
      (f.finds.last['searchParams'] as List).single['conversationID'],
      f.repository.conversationId,
    );
  });

  test('saved emoji real GIF staging is explicit opt in and preserves both animated frames', () async {
    final bytes = twoFrameEmojiGif();
    final file = File('${f.sdk.directory.path}/animated-original.gif');
    await file.writeAsBytes(bytes);
    final stager = ImageStager(f.sdk.gateway, f.access);
    await expectLater(stager.prepare(file.path), throwsA(isA<AppFailure>()));
    final staged = await stager.prepare(file.path, allowGif: true);
    try {
      expect(staged.contentType, 'image/gif');
      expect(staged.extension, '.gif');
      expect(await staged.file.readAsBytes(), bytes);
      final codec = await ui.instantiateImageCodec(
        await staged.file.readAsBytes(),
      );
      try {
        expect(codec.frameCount, 2);
      } finally {
        codec.dispose();
      }
    } finally {
      await staged.dispose();
    }
    expect(await file.readAsBytes(), bytes);
  });

  test('saved emoji preserves existing conversation cannot send admission before Face creation', () async {
    f.sdk.conversations.single['isNotInGroup'] = true;
    await f.repository.conversation();
    final cut = f.sdk.gateway.historyAccess(
      f.repository.generation,
      conversationId: f.repository.conversationId,
    );
    final result = await f.controller.sendSavedEmoji(
      f.uploadEntry(),
      access: cut,
      checkSource: () {},
    );
    expect(result, SavedEmojiSendOutcome.notDispatched);
    expect(f.faces, 0);
    expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
  });
}
