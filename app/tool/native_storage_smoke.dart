// Development-only entry point. Uses an isolated secure-storage namespace and
// temporary database; never reads production credentials or starts OpenIM.
import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:imapp/core/storage/secure_credential_store.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/personal/data/personal_archive.dart';
import 'package:imapp/features/personal/data/personal_repository.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';
import 'package:path_provider/path_provider.dart';
import 'package:sqflite/sqflite.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(const MaterialApp(home: StorageSmokePage()));
}

class StorageSmokePage extends StatefulWidget {
  const StorageSmokePage({super.key});
  @override
  State<StorageSmokePage> createState() => _StorageSmokePageState();
}

class _StorageSmokePageState extends State<StorageSmokePage> {
  String result = 'Checking isolated native storage…';
  @override
  void initState() {
    super.initState();
    unawaited(_check());
  }

  Future<void> _check() async {
    const storage = FlutterSecureStorage(
      iOptions: IOSOptions(accountName: 'luobo-native-smoke'),
      aOptions: AndroidOptions(storageNamespace: 'luobo_native_smoke'),
    );
    final credentials = SessionCredentials(
      userId: 'native-smoke',
      tenantId: 'native-smoke',
      tenantName: 'native-smoke',
      domains: ['tenant.invalid'],
      chatToken: 'fixture-chat',
      imToken: 'fixture-im',
    );
    final store = SecureCredentialStore(storage);
    PersonalRepository? repository;
    Directory? temporary;
    var passed = false;
    try {
      await store.clear();
      await store.write(credentials);
      if ((await store.read())?.accountKey != credentials.accountKey) {
        throw StateError('secure read mismatch');
      }
      await store.clear();
      if (await store.read() != null) throw StateError('secure clear mismatch');
      final support = await getApplicationSupportDirectory();
      temporary = await Directory(
        '${support.path}/native-smoke-${DateTime.now().microsecondsSinceEpoch}',
      ).create();
      final databasePath = '${temporary.path}/personal.sqlite3';
      repository = PersonalRepository(
        accountKey: credentials.accountKey,
        isCurrent: () => true,
        open: (_) => initializePersonalDatabase(databaseFactory, databasePath),
      );
      final note = PersonalItem.create(PersonalKind.note)
          .copyWith(title: 'native fixture');
      await repository.save(note);
      if ((await repository.list()).single.title != note.title) {
        throw StateError('SQLite read mismatch');
      }
      final backup = PersonalArchive.decode(await repository.export());
      await repository.delete(note);
      if (await repository.import(backup, confirmOwnership: false) != 1) {
        throw StateError('SQLite import mismatch');
      }
      if (await repository.import(backup, confirmOwnership: false) != 0) {
        throw StateError('SQLite idempotency mismatch');
      }
      passed = true;
    } catch (_) {
      passed = false;
    } finally {
      await repository?.close();
      try {
        await store.clear();
      } catch (_) {
        passed = false;
      }
      try {
        await temporary?.delete(recursive: true);
      } catch (_) {
        passed = false;
      }
    }
    final marker = passed
        ? 'LUOBO_APP_STORAGE_SMOKE_PASS'
        : 'LUOBO_APP_STORAGE_SMOKE_FAIL';
    debugPrint(marker);
    if (mounted) setState(() => result = marker);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: SafeArea(
      child: Center(
        child: Padding(padding: const EdgeInsets.all(24), child: Text(result)),
      ),
    ),
  );
}
