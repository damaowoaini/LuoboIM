import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ChatRepository repository;
  late Directory sources;
  setUp(() async {
    harness = SdkHarness();
    harness.conversations.add(SdkHarness.conversation());
    await harness.start();
    repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'single-alice-bob',
    );
    await repository.conversation();
    sources = await Directory.systemTemp.createTemp('luobo-picker-test-');
    harness.overrideCall = (call) async {
      final args = call.arguments as Map;
      if (call.method == 'luoboVideoMetadata') {
        await File(args['thumbnailPath'] as String)
            .writeAsBytes([255, 216, 255, 217]);
        return {
          'durationMilliseconds': 1001,
          'width': 32,
          'height': 32,
          'thumbnailPath': args['thumbnailPath'],
        };
      }
      if (call.method.startsWith('create') &&
          call.method.endsWith('FromFullPath')) {
        final message = SdkHarness.message(
          id: 'attachment-id',
          text: '',
          sender: 'alice',
          status: 1,
        );
        if (call.method == 'createFileMessageFromFullPath') {
          message['contentType'] = 105;
          message['fileElem'] = {'fileName': args['fileName'], 'fileSize': 4};
        } else if (call.method == 'createVideoMessageFromFullPath') {
          message['contentType'] = 104;
          message['videoElem'] = {'duration': args['duration']};
        } else {
          message['contentType'] = 102;
          message['pictureElem'] = <String, dynamic>{};
        }
        return jsonEncode(message);
      }
      return harness.respond(call);
    };
  });
  tearDown(() async {
    await harness.dispose();
    await sources.delete(recursive: true);
  });

  test('picker files are copied into the account and retained for upload retry with one SDK ID', () async {
    final source = await File('${sources.path}/report.txt')
        .writeAsBytes([1, 2, 3, 4]);
    final item = await repository.prepareAttachment(
      ChatAttachment(
        kind: AttachmentKind.file,
        path: source.path,
        name: '报表\n.txt',
      ),
    );
    expect(
      item.localMedia,
      startsWith(
        '${harness.directory.path}/openim/${harness.credentials.accountKey}/media/',
      ),
    );
    expect(item.localMedia, isNot(source.path));
    expect(item.fileName, '报表_.txt');
    await source.delete();
    expect(await File(item.localMedia!).readAsBytes(), [1, 2, 3, 4]);
    var first = true;
    final standard = harness.overrideCall!;
    harness.overrideCall = (call) async {
      if (call.method == 'sendMessage' && first) {
        first = false;
        throw PlatformException(code: '999');
      }
      return standard(call);
    };
    await expectLater(repository.send(item.id), throwsA(isA<AppFailure>()));
    final sent = await repository.send(item.id);
    expect(sent.localMedia, item.localMedia);
    expect(
      harness.calls.where(
        (call) => call.method == 'createFileMessageFromFullPath',
      ),
      hasLength(1),
    );
    expect(
      harness.calls
          .where((call) => call.method == 'sendMessage')
          .map((call) => call.arguments['message']['clientMsgID']),
      [item.id, item.id],
    );
  });

  test('video metadata is rounded to seconds and the thumbnail remains account scoped', () async {
    final source = await File('${sources.path}/clip.mov')
        .writeAsBytes([1, 2, 3, 4]);
    final item = await repository.prepareAttachment(
      ChatAttachment(
        kind: AttachmentKind.video,
        path: source.path,
        name: 'clip.mov',
      ),
    );
    final call = harness.calls.singleWhere(
      (call) => call.method == 'createVideoMessageFromFullPath',
    );
    expect(call.arguments['duration'], 2);
    expect(call.arguments['videoType'], 'video/quicktime');
    expect(item.localThumbnail, call.arguments['snapshotPath']);
    expect(await File(item.localThumbnail!).exists(), isTrue);
  });

  test('logout while video inspection is pending rejects the old result and removes its staged files', () async {
    final entered = Completer<MethodCall>();
    final release = Completer<void>();
    final standard = harness.overrideCall!;
    harness.overrideCall = (call) async {
      if (call.method == 'luoboVideoMetadata') {
        entered.complete(call);
        await release.future;
      }
      return standard(call);
    };
    final source = await File('${sources.path}/clip.mp4')
        .writeAsBytes([1, 2, 3, 4]);
    final pending = repository.prepareAttachment(
      ChatAttachment(
        kind: AttachmentKind.video,
        path: source.path,
        name: 'clip.mp4',
      ),
    );
    final expectation = expectLater(pending, throwsA(isA<AppFailure>()));
    final call = await entered.future;
    await harness.session.signOut();
    release.complete();
    await expectation;
    expect(
      harness.calls.where(
        (call) => call.method == 'createVideoMessageFromFullPath',
      ),
      isEmpty,
    );
    expect(await File(call.arguments['videoPath'] as String).exists(), isFalse);
    expect(
      await File(call.arguments['thumbnailPath'] as String).exists(),
      isFalse,
    );
    expect(await source.exists(), isTrue);
  });

  test('empty media and out-of-range voice duration never reach native message creation', () async {
    final empty = await File('${sources.path}/empty').writeAsBytes([]);
    await expectLater(
      repository.prepareAttachment(
        ChatAttachment(
          kind: AttachmentKind.image,
          path: empty.path,
          name: 'empty',
        ),
      ),
      throwsA(isA<AppFailure>()),
    );
    final voice = await File('${sources.path}/voice.m4a').writeAsBytes([1]);
    for (final duration in [0, 61]) {
      await expectLater(
        repository.prepareAttachment(
          ChatAttachment(
            kind: AttachmentKind.voice,
            path: voice.path,
            name: 'voice',
            durationSeconds: duration,
          ),
        ),
        throwsA(isA<AppFailure>()),
      );
    }
    expect(
      harness.calls.where((call) => call.method.endsWith('FromFullPath')),
      isEmpty,
    );
  });
}
