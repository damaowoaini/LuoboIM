import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:crypto/crypto.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/storage/staged_image.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../../core/tenant/session_credentials.dart';

final class _MalformedPointer {
  const _MalformedPointer(this.account, this.cid, this.digest);
  final String account, cid, digest;
}

final class _PointerBytes {
  const _PointerBytes(this.digest, this.body);
  final String digest;
  final List<int>? body;
}

final class ChatBackgroundSnapshot {
  const ChatBackgroundSnapshot(this.revision, this.image, {this.unavailable = false})
      : _pointerDigest = null, _repair = null;
  const ChatBackgroundSnapshot._read(this.revision, this.image, this._pointerDigest,
      {this.unavailable = false}) : _repair = null;
  ChatBackgroundSnapshot._malformed(_MalformedPointer repair)
      : revision = 'malformed', image = null, unavailable = true,
        _pointerDigest = repair.digest, _repair = repair;
  static const empty = ChatBackgroundSnapshot('absent', null);
  final String revision;
  final File? image;
  final bool unavailable;
  final String? _pointerDigest;
  final _MalformedPointer? _repair;
  bool get requiresDefaultRestore => _repair != null;
}

final class ChatBackgroundChange {
  const ChatBackgroundChange(this.accountKey, this.conversationId, this.snapshot);
  final String accountKey, conversationId;
  final ChatBackgroundSnapshot snapshot;
}

/// Only this feature's exact generated paths are ever removed. Picker originals
/// and SDK media caches are inputs, not persistent background storage.
final class ChatBackgroundStore {
  ChatBackgroundStore({required this.supportDirectory,
    Future<void> Function(File temporary, File destination)? replacePointer})
      : _replacePointer = replacePointer ?? _rename;
  final Future<Directory> Function() supportDirectory;
  final Future<void> Function(File, File) _replacePointer;
  static final _random = Random.secure();
  static final _id = RegExp(r'^[a-f0-9]{32}$');
  static final _imageName = RegExp(r'^image_[a-f0-9]{32}\.(png|jpg|jpeg|webp|heic|heif)$');
  final _changes = StreamController<ChatBackgroundChange>.broadcast(sync: true);
  Future<void> _tail = Future<void>.value();
  bool _disposed = false;
  Stream<ChatBackgroundChange> get changes => _changes.stream;
  static Future<void> _rename(File from, File to) async { await from.rename(to.path); }
  static String _nonce() => List.generate(16,
    (_) => _random.nextInt(256).toRadixString(16).padLeft(2, '0')).join();
  static void _check(bool Function() current) {
    if (!current()) throw const AppFailure(FailureKind.cancelled);
  }
  Future<T> _ordered<T>(bool Function() current, Future<T> Function() action) {
    final result = _tail.then((_) async {
      if (_disposed) throw const AppFailure(FailureKind.cancelled);
      _check(current);
      try { return await action(); }
      on AppFailure { rethrow; }
      catch (_) { throw const AppFailure(FailureKind.localStorage); }
    });
    _tail = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }
  Future<Directory?> _directory(String account, String cid,
      bool Function() current, {required bool create}) async {
    if (!RegExp(r'^[a-f0-9]{64}$').hasMatch(account) || cid.isEmpty || cid.length > 1024 ||
        utf8.decode(utf8.encode(cid)) != cid) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final support = await supportDirectory(); _check(current);
    var parent = Directory(await support.resolveSymbolicLinks()); _check(current);
    for (final segment in ['chat_backgrounds_v1', account, sha256.convert(utf8.encode(cid)).toString()]) {
      final next = Directory('${parent.path}/$segment');
      var type = await FileSystemEntity.type(next.path, followLinks: false); _check(current);
      if (type == FileSystemEntityType.notFound) {
        if (!create) return null;
        await next.create(); _check(current);
        type = await FileSystemEntity.type(next.path, followLinks: false); _check(current);
      }
      if (type != FileSystemEntityType.directory || await next.resolveSymbolicLinks() != next.path) {
        throw const AppFailure(FailureKind.localStorage);
      }
      _check(current); parent = next;
    }
    return parent;
  }

  /// Read the exact regular pointer, not a guessed default or a symlink target.
  /// Oversized malformed records are hashed as a stream, never loaded whole.
  Future<_PointerBytes?> _rawPointer(Directory directory, bool Function() current) async {
    if (await FileSystemEntity.type(directory.path, followLinks: false) != FileSystemEntityType.directory ||
        await directory.resolveSymbolicLinks() != directory.path) {
      throw const AppFailure(FailureKind.localStorage);
    }
    _check(current);
    final pointer = File('${directory.path}/background.json');
    final type = await FileSystemEntity.type(pointer.path, followLinks: false); _check(current);
    if (type == FileSystemEntityType.notFound) return null;
    if (type != FileSystemEntityType.file) throw const AppFailure(FailureKind.localStorage);
    final length = await pointer.length(); _check(current);
    if (length <= 4096) {
      final bytes = await pointer.readAsBytes(); _check(current);
      if (bytes.length != length) throw const AppFailure(FailureKind.dataChanged);
      return _PointerBytes(sha256.convert(bytes).toString(), bytes);
    }
    var observed = 0;
    final digest = await sha256.bind(pointer.openRead().map((bytes) {
      _check(current); observed += bytes.length; return bytes;
    })).first;
    _check(current);
    if (observed != length) throw const AppFailure(FailureKind.dataChanged);
    return _PointerBytes(digest.toString(), null);
  }
  ChatBackgroundSnapshot _malformed(String account, String cid,
      _PointerBytes raw, bool allowRepair) {
    if (!allowRepair) throw const AppFailure(FailureKind.localStorage);
    return ChatBackgroundSnapshot._malformed(_MalformedPointer(account, cid, raw.digest));
  }
  Future<ChatBackgroundSnapshot> _read(Directory? directory,
      bool Function() current, String account, String cid, {bool allowRepair = false}) async {
    if (directory == null) return ChatBackgroundSnapshot.empty;
    final raw = await _rawPointer(directory, current); _check(current);
    if (raw == null) return ChatBackgroundSnapshot.empty;
    Object? decoded;
    try {
      if (raw.body == null) return _malformed(account, cid, raw, allowRepair);
      decoded = jsonDecode(utf8.decode(raw.body!));
    } on FormatException {
      return _malformed(account, cid, raw, allowRepair);
    }
    if (decoded is! Map<String, dynamic> || decoded['version'] != 1 ||
        decoded['revision'] is! String || !_id.hasMatch(decoded['revision'] as String) ||
        !decoded.containsKey('image')) {
      return _malformed(account, cid, raw, allowRepair);
    }
    final value = decoded;
    if (value['image'] == null) {
      return ChatBackgroundSnapshot._read(value['revision'] as String, null, raw.digest);
    }
    final name = value['image'], bytes = value['bytes'], expected = value['sha256'];
    if (name is! String || !_imageName.hasMatch(name) || bytes is! int ||
        bytes < 1 || bytes > 20 * 1024 * 1024 || expected is! String ||
        !RegExp(r'^[a-f0-9]{64}$').hasMatch(expected)) {
      return _malformed(account, cid, raw, allowRepair);
    }
    final image = File('${directory.path}/$name');
    final imageType = await FileSystemEntity.type(image.path, followLinks: false); _check(current);
    if (imageType != FileSystemEntityType.file) {
      return ChatBackgroundSnapshot._read(value['revision'] as String, image, raw.digest, unavailable: true);
    }
    final length = await image.length(); _check(current);
    if (length != bytes) {
      return ChatBackgroundSnapshot._read(value['revision'] as String, image, raw.digest, unavailable: true);
    }
    final body = await image.readAsBytes(); _check(current);
    if (body.length != bytes || sha256.convert(body).toString() != expected) {
      return ChatBackgroundSnapshot._read(value['revision'] as String, image, raw.digest, unavailable: true);
    }
    return ChatBackgroundSnapshot._read(value['revision'] as String, image, raw.digest);
  }
  Future<ChatBackgroundSnapshot> read(String account, String cid,
      {required bool Function() current}) => _ordered(current, () async {
    final directory = await _directory(account, cid, current, create: false);
    return _read(directory, current, account, cid);
  });
  Future<ChatBackgroundSnapshot> readForEditor(String account, String cid,
      {required bool Function() current}) => _ordered(current, () async {
    final directory = await _directory(account, cid, current, create: false);
    return _read(directory, current, account, cid, allowRepair: true);
  });
  Future<ChatBackgroundSnapshot> save(String account, String cid,
      {required String expectedRevision, required StagedImage? selected,
       required bool Function() current}) => _ordered(current, () async {
    final directory = (await _directory(account, cid, current, create: true))!;
    final previous = await _read(directory, current, account, cid); _check(current);
    if (previous.revision != expectedRevision) throw const AppFailure(FailureKind.dataChanged);
    return _commit(account, cid, directory, previous, selected, current);
  });

  /// This is a distinct, explicit default-only intent. Ordinary image saves
  /// never accept malformed metadata or borrow this raw-byte revision ticket.
  Future<ChatBackgroundSnapshot> restoreMalformedDefault(String account, String cid,
      ChatBackgroundSnapshot expected, {required bool Function() current}) => _ordered(current, () async {
    final ticket = expected._repair;
    if (ticket == null || ticket.account != account || ticket.cid != cid) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final directory = await _directory(account, cid, current, create: false);
    if (directory == null) throw const AppFailure(FailureKind.dataChanged);
    final raw = await _rawPointer(directory, current); _check(current);
    if (raw == null || raw.digest != ticket.digest) throw const AppFailure(FailureKind.dataChanged);
    return _commit(account, cid, directory, expected, null, current);
  });

  Future<ChatBackgroundSnapshot> _commit(String account, String cid, Directory directory,
      ChatBackgroundSnapshot previous, StagedImage? selected, bool Function() current) async {
    _check(current);
    final revision = _nonce();
    final record = <String, Object?>{'version': 1, 'revision': revision, 'image': null};
    File? image, temporary;
    bool replacing = false, committed = false, imageCreated = false, temporaryCreated = false;
    try {
      if (selected != null) {
        final extension = selected.extension;
        if (!const ['.png', '.jpg', '.jpeg', '.webp', '.heic', '.heif'].contains(extension) ||
            selected.width < 1 || selected.height < 1 || selected.width * selected.height > 100000000 ||
            await FileSystemEntity.type(selected.file.path, followLinks: false) != FileSystemEntityType.file) {
          throw const AppFailure(FailureKind.invalidInput);
        }
        _check(current);
        final length = await selected.file.length(); _check(current);
        if (length < 1 || length > 20 * 1024 * 1024) throw const AppFailure(FailureKind.invalidInput);
        final body = await selected.file.readAsBytes(); _check(current);
        if (body.length != length) throw const AppFailure(FailureKind.dataChanged);
        final name = 'image_$revision$extension';
        image = File('${directory.path}/$name');
        await image.create(exclusive: true); imageCreated = true; _check(current);
        await image.writeAsBytes(body, flush: true); _check(current);
        record.addAll({'image': name, 'bytes': body.length, 'sha256': sha256.convert(body).toString()});
      }
      temporary = File('${directory.path}/pending_$revision.json');
      await temporary.create(exclusive: true); temporaryCreated = true; _check(current);
      final encoded = jsonEncode(record);
      await temporary.writeAsString(encoded, flush: true); _check(current);
      replacing = true;
      await _replacePointer(temporary, File('${directory.path}/background.json'));
      committed = true;
      final result = ChatBackgroundSnapshot._read(revision, image, sha256.convert(utf8.encode(encoded)).toString());
      if (!_disposed) _changes.add(ChatBackgroundChange(account, cid, result));
      if (previous.image != null) await _deleteOwned(previous.image!, directory);
      _check(current);
      return result;
    } finally {
      if (image != null && imageCreated && !committed) {
        var discard = !replacing;
        if (replacing) {
          // Replacement Future has TERMINATED. Read only this frozen directory;
          // exact old bytes prove the old revision still owns the pointer and
          // cannot reference this operation's new immutable image. No new lease.
          try {
            final after = await _rawPointer(directory, () => true);
            discard = after?.digest == previous._pointerDigest &&
              previous.revision != revision && previous.image?.path != image.path;
          } catch (_) { discard = false; }
        }
        if (discard) await _deleteOwned(image, directory);
      }
      if (temporary != null && temporaryCreated) await _deleteOwned(temporary, directory);
    }
  }
  static Future<void> _deleteOwned(File file, Directory directory) async {
    final name = file.uri.pathSegments.last;
    if (file.parent.path != directory.path ||
        !(_imageName.hasMatch(name) || RegExp(r'^pending_[a-f0-9]{32}\.json$').hasMatch(name))) {
      return;
    }
    try {
      if (await FileSystemEntity.type(directory.path, followLinks: false) != FileSystemEntityType.directory ||
          await directory.resolveSymbolicLinks() != directory.path) {
        return;
      }
      if (await FileSystemEntity.type(file.path, followLinks: false) == FileSystemEntityType.file) {
        await file.delete();
      }
    } catch (_) { /* Cleanup cannot turn an acknowledged commit into a failed save. */ }
  }
  Future<void> flush() => _tail;
  void dispose() { _disposed = true; unawaited(_changes.close()); }
}

final class ChatBackgroundPreferences {
  ChatBackgroundPreferences(this.store, this.session, this.conversationId)
      : _owner = session.state.credentials, _generation = session.state.generation,
        _interactive = session.interactiveRevision,
        _issuer = session.state.credentials?.issuer, _tenant = session.state.credentials?.tenantId;
  final ChatBackgroundStore store;
  final SessionCoordinator session;
  final String conversationId;
  final SessionCredentials? _owner;
  final int _generation, _interactive;
  final String? _issuer, _tenant;
  String get accountKey => _owner?.accountKey ?? '';
  bool get current => _owner != null && identical(session.state.credentials, _owner) &&
      session.state.credentials?.issuer == _issuer && session.state.credentials?.tenantId == _tenant &&
      session.isInteractiveCurrent(_generation, _interactive);
  Future<ChatBackgroundSnapshot> read() => store.read(accountKey, conversationId, current: () => current);
  Future<ChatBackgroundSnapshot> readForEditor() => store.readForEditor(accountKey, conversationId, current: () => current);
  Future<ChatBackgroundSnapshot> save(ChatBackgroundSnapshot previous, StagedImage? image,
      {required bool Function() sourceCurrent}) => store.save(accountKey, conversationId,
        expectedRevision: previous.revision, selected: image, current: () => current && sourceCurrent());
  Future<ChatBackgroundSnapshot> restoreDefault(ChatBackgroundSnapshot previous,
      {required bool Function() sourceCurrent}) => previous.requiresDefaultRestore
        ? store.restoreMalformedDefault(accountKey, conversationId, previous,
            current: () => current && sourceCurrent())
        : save(previous, null, sourceCurrent: sourceCurrent);
}
