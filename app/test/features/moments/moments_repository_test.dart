import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/moments/data/moments_repository.dart';
import 'package:imapp/features/moments/domain/moment.dart';

import '../../core/privacy_bootstrap_test.dart' show StoreProbe, ImProbe;
import '../../support/http_adapter.dart';

Map<String, Object?> item(String id, {String author = 'alice'}) => {
  'workMomentID': id,
  'userID': author,
  'nickname': '显示名称',
  'content': {'text': '原始内容', 'type': 0, 'metas': []},
  'permission': 0,
  'likeUsers': [],
  'comments': [],
  'permissionUsers': [],
  'atUsers': [],
  'createTime': '1700000000000',
};

void main() {
  late SessionCoordinator session;
  late TenantApi api;
  late TestHttpAdapter http;
  late MomentsRepository repository;
  late Future<Object?> Function(RequestOptions) respond;
  setUp(() async {
    session = SessionCoordinator(store: StoreProbe(), im: ImProbe());
    await session.signIn(
      (_) async => SessionCredentials(
        userId: 'alice',
        tenantId: 'tenant-a',
        tenantName: '测试服务',
        domains: ['im.example.com'],
        chatToken: 'test-chat-only',
        imToken: 'test-im-only',
      ),
    );
    respond = (request) async {
      final op = request.uri.path.replaceFirst('/chat/office/work_moment/', '');
      return switch (op) {
        'find/recv' => {
          'workMoments': [item('one', author: 'bob')],
        },
        'find/send' => {
          'workMoments': [
            item('one', author: request.data['userID'] as String),
          ],
        },
        'add' || 'get' => {'workMoment': item('one')},
        'logs' => {
          'workMoments': [
            {...item('one'), 'type': 3},
          ],
        },
        'unread/count' => {'total': 4},
        _ => null,
      };
    };
    http = TestHttpAdapter(
      (request) async => jsonResponse(
        jsonEncode({'errCode': 0, 'data': await respond(request)}),
      ),
    );
    api = TenantApi(
      SessionAccess(session),
      dio: Dio()..httpClientAdapter = http,
    );
    repository = MomentsRepository(api);
  });
  tearDown(() async {
    api.close();
    await session.dispose();
  });

  test('legacy endpoints use chat token and frozen actor, with server-resolved feed friends', () async {
    await repository.list();
    final feed = http.requests.last.data as Map;
    expect(feed['userID'], 'alice');
    expect(feed.containsKey('friendIDs'), isFalse);
    await repository.list(author: 'bob');
    await repository.create(MomentDraft(text: '发布'));
    await repository.detail('one');
    await repository.like('one', true);
    await repository.comment('one', '回复', replyTo: 'bob');
    final comment = MomentComment(
      id: 'comment',
      author: const MomentUser(id: 'alice'),
      text: '回复',
    );
    final moment = Moment(
      id: 'one',
      author: const MomentUser(id: 'alice'),
      text: '内容',
      visibility: MomentVisibility.public,
      mediaType: 0,
      comments: [comment],
    );
    await repository.deleteComment(moment, comment);
    await repository.delete(moment);
    expect((await repository.interactions()).single.type, 3);
    expect(await repository.unreadCount(), 4);
    await repository.clearUnread(type: 3);
    expect(
      http.requests
          .map(
            (request) =>
                request.uri.path.replaceFirst('/chat/office/work_moment/', ''),
          )
          .toSet(),
      {
        'find/recv',
        'find/send',
        'add',
        'get',
        'like',
        'comment/add',
        'comment/del',
        'del',
        'logs',
        'unread/count',
        'unread/clear',
      },
    );
    for (final request in http.requests) {
      expect(request.uri.host, 'im.example.com');
      expect(request.headers['token'], 'test-chat-only');
    }
  });

  test('all four visibility codes remain distinct and restricted audiences cannot silently become public', () async {
    for (final visibility in MomentVisibility.values) {
      await repository.create(
        MomentDraft(
          text: '内容',
          visibility: visibility,
          audience: const ['bob', 'bob'],
        ),
      );
      expect(http.requests.last.data['permission'], visibility.code);
      expect(http.requests.last.data['permissionUserIDs'], ['bob']);
    }
    final before = http.requests.length;
    await expectLater(
      repository.create(
        MomentDraft(text: '内容', visibility: MomentVisibility.included),
      ),
      throwsA(isA<AppFailure>()),
    );
    expect(http.requests.length, before);
  });

  test('local paths, insecure attachments, unsupported video batches and empty drafts never publish', () async {
    for (final draft in [
      MomentDraft(text: ''),
      MomentDraft(
        text: '内容',
        media: [const MomentMedia(original: '/private/image.jpg')],
      ),
      MomentDraft(
        text: '内容',
        media: [
          const MomentMedia(original: 'http://media.example.com/image.jpg'),
        ],
      ),
      MomentDraft(
        text: '内容',
        media: [
          const MomentMedia(
            original: 'https://user:password@media.example.com/image.jpg',
          ),
        ],
      ),
      MomentDraft(
        text: '内容',
        mediaType: 1,
        media: List.filled(
          2,
          const MomentMedia(original: 'https://media.example.com/movie.mp4'),
        ),
      ),
    ]) {
      await expectLater(repository.create(draft), throwsA(isA<AppFailure>()));
    }
    expect(http.requests, isEmpty);
    final draft = MomentDraft(
      text: '图片',
      media: [
        const MomentMedia(
          original: 'https://media.example.com/p.jpg',
          width: 640,
          height: 480,
        ),
      ],
    );
    await repository.create(draft);
    expect(http.requests.single.data['content']['metas'][0]['width'], 640);
  });

  test('foreign author or mismatched source IDs fail closed and invalid pagination does not request', () async {
    respond = (_) async => {'workMoment': item('another', author: 'bob')};
    await expectLater(repository.detail('one'), throwsA(isA<AppFailure>()));
    await expectLater(
      repository.create(MomentDraft(text: '内容')),
      throwsA(isA<AppFailure>()),
    );
    respond = (_) async => {
      'workMoments': [item('one')],
    };
    await expectLater(
      repository.list(author: 'bob'),
      throwsA(isA<AppFailure>()),
    );
    final before = http.requests.length;
    await expectLater(repository.list(page: 0), throwsA(isA<AppFailure>()));
    await expectLater(
      repository.clearUnread(type: 0),
      throwsA(isA<AppFailure>()),
    );
    expect(http.requests.length, before);
  });

  test('duplicate page IDs and invalid counters are not shown as successful empty state', () async {
    respond = (_) async => {
      'workMoments': [item('one'), item('one')],
    };
    await expectLater(repository.list(), throwsA(isA<AppFailure>()));
    respond = (_) async => {'total': -1};
    await expectLater(repository.unreadCount(), throwsA(isA<AppFailure>()));
  });

  test(
    'late mutation cannot refresh or leak data into the next account',
    () async {
      final gate = Completer<Object?>();
      final entered = Completer<void>();
      respond = (_) {
        entered.complete();
        return gate.future;
      };
      final pending = repository.like('one', true);
      final assertion = expectLater(pending, throwsA(isA<AppFailure>()));
      await entered.future;
      await session.signOut();
      gate.complete(null);
      await assertion;
      expect(http.requests, hasLength(1));
    },
  );

  test('comment deletion uses owner, comment author or reply recipient; other viewers never send deletion', () async {
    const comment = MomentComment(
      id: 'comment',
      author: MomentUser(id: 'bob'),
      text: '原评论',
    );
    const foreign = Moment(
      id: 'one',
      author: MomentUser(id: 'bob'),
      text: '内容',
      visibility: MomentVisibility.public,
      mediaType: 0,
      comments: [comment],
    );
    await expectLater(repository.delete(foreign), throwsA(isA<AppFailure>()));
    await expectLater(
      repository.deleteComment(foreign, comment),
      throwsA(isA<AppFailure>()),
    );
    expect(http.requests, isEmpty);
    expect(foreign.canDeleteComment('bob', comment), isTrue);
  });

  test('old missing lists and protobuf string timestamps retain text while unsafe media stays unrenderable', () {
    final value = item('old');
    value.remove('comments');
    value.remove('likeUsers');
    value['content'] = {
      'text': '历史正文',
      'type': 1,
      'metas': [
        {'original': 'http://legacy.example/movie.mp4'},
      ],
    };
    final moment = Moment.fromJson(value);
    expect(moment.text, '历史正文');
    expect(moment.comments, isEmpty);
    expect(moment.createdAt!.millisecondsSinceEpoch, 1700000000000);
    expect(moment.media.single.uri, isNull);
    expect(() => MomentVisibility.parse(99), throwsA(isA<AppFailure>()));
  });
}
