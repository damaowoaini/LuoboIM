import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path_provider/path_provider.dart';
import 'package:sqflite/sqflite.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/network/tenant_api.dart';
import 'data/booking_contract.dart';
import 'data/meeting_booking_repository.dart';
import 'data/tenant_booking_adapter.dart';
import 'presentation/booking_controller.dart';

final class BookingReleaseCapability {
  const BookingReleaseCapability({required this.issuer, required this.tenantId,
    required this.imBase, this.contractVersion = 1});
  final String issuer, tenantId, imBase;
  final int contractVersion;
  bool matches(TenantApi api) => contractVersion == 1 && issuer == api.access.credentials.issuer &&
    tenantId == api.access.credentials.tenantId && imBase == api.im.base.toString();
}

// Release-owned exact catalog, intentionally empty. No URL parameter, form,
// server error text or legacy room can activate booking writes.
final bookingReleaseCatalogProvider = Provider<List<BookingReleaseCapability>>((_) => const []);
final bookingDatabaseFactoryProvider = Provider<DatabaseFactory>((_) => databaseFactory);
final bookingPrivatePathProvider = Provider<Future<String> Function(String)>((_) => bookingPrivatePath);

Future<String> bookingPrivatePath(String accountKey) async {
  if (!RegExp(r'^[0-9a-f]{64}$').hasMatch(accountKey)) {
    throw const BookingLocalFailure(BookingLocalFailureKind.storage);
  }
  final support = await getApplicationSupportDirectory();
  final canonical = await support.resolveSymbolicLinks();
  var parent = Directory(canonical);
  for (final part in ['meeting-bookings', 'v1', accountKey]) {
    final next = Directory('${parent.path}${Platform.pathSeparator}$part');
    final type = await FileSystemEntity.type(next.path, followLinks: false);
    if (type != FileSystemEntityType.notFound && type != FileSystemEntityType.directory) {
      throw const BookingLocalFailure(BookingLocalFailureKind.storage);
    }
    if (type == FileSystemEntityType.notFound) await next.create();
    if (await next.resolveSymbolicLinks() != next.absolute.path) {
      throw const BookingLocalFailure(BookingLocalFailureKind.storage);
    }
    parent = next;
  }
  final path = '${parent.path}${Platform.pathSeparator}journal.sqlite';
  final type = await FileSystemEntity.type(path, followLinks: false);
  if (type != FileSystemEntityType.notFound && type != FileSystemEntityType.file) {
    throw const BookingLocalFailure(BookingLocalFailureKind.storage);
  }
  // Android/iOS app-support sandbox is the OS boundary. Desktop must already
  // have private permissions; do not spawn chmod or silently accept public files.
  if (!Platform.isAndroid && !Platform.isIOS) {
    if (Platform.isWindows || ((await parent.stat()).mode & 0x3f) != 0 ||
        (type == FileSystemEntityType.file && ((await File(path).stat()).mode & 0x3f) != 0)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.storage);
    }
  }
  return path;
}

final bookingControllerProvider = FutureProvider.autoDispose<BookingController>((ref) async {
  final api = ref.watch(tenantApiProvider);
  if (api == null) throw const AppFailure(FailureKind.sessionExpired);
  final access = api.access, credentials = access.credentials;
  final released = ref.watch(bookingReleaseCatalogProvider).any((entry) => entry.matches(api));
  final factory = ref.watch(bookingDatabaseFactoryProvider);
  final pathLoader = ref.watch(bookingPrivatePathProvider);
  var disposed = false;
  MeetingBookingRepository? repository;
  BookingController? controller;
  void check() {
    access.check();
    if (disposed || !identical(access.session.state.credentials, credentials)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.staleSession);
    }
  }
  Future<void> closeOwned() async {
    final owned = repository;
    if (owned != null) await owned.journal.close();
  }
  // Synchronous registration precedes both OS-path and SQLite awaits.
  ref.onDispose(() {
    disposed = true; controller?.dispose(); repository?.scope.close();
    unawaited(closeOwned().catchError((Object _) {
      FlutterError.reportError(FlutterErrorDetails(
        exception: const BookingLocalFailure(BookingLocalFailureKind.storage),
        library: 'owner booking cleanup',
      ));
    }));
  });
  try {
    check(); final path = await pathLoader(credentials.accountKey); check();
    repository = await openTenantBookingRepository(api, factory, path, checkAlive: check);
    check();
    controller = BookingController(repository!, released: released,
      invalidations: access.session.invalidations, accessChanges: access.session.accessChanges);
    await controller!.start(); check(); return controller!;
  } catch (_) {
    controller?.dispose(); await closeOwned(); rethrow;
  }
});
