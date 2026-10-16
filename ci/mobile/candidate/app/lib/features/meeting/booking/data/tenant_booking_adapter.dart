// Owner-scoped tokenless booking adapter; no SDK Create/Join fallback.
// Existing locked imapp dependencies and immutable TenantApi are reused.
import 'package:imapp/core/network/tenant_api.dart';
import 'package:sqflite/sqflite.dart';

import 'booking_pending_journal.dart';
import 'meeting_booking_repository.dart';

final class TenantBookingTransport implements BookingTransport {
  TenantBookingTransport(this.api, this.checkAlive);
  final void Function() checkAlive;
  final TenantApi api;
  @override
  Future<Object?> post(String relativePath, Map<String, Object?> body) async {
    checkAlive();
    api.access.check();
    final result = await api.im.post(relativePath, body);
    checkAlive();
    api.access.check();
    return result;
  }
}

Future<MeetingBookingRepository> openTenantBookingRepository(
  TenantApi api,
  DatabaseFactory factory,
  String applicationPrivateDatabasePath, {
  required void Function() checkAlive,
}) async {
  final access = api.access; // captured once; never acquire a later session
  void check() { checkAlive(); access.check(); }
  check();
  final scope = BookingJournalScope(
    accountKey: access.credentials.accountKey,
    issuer: access.credentials.issuer,
    tenantId: access.credentials.tenantId,
    owner: access.credentials.userId,
    checkCurrent: check,
  );
  final journal = await BookingPendingJournal.open(
    factory,
    applicationPrivateDatabasePath,
    scope,
  );
  try {
    check();
    return MeetingBookingRepository(
      transport: TenantBookingTransport(api, check),
      journal: journal,
    );
  } catch (_) {
    await journal.close();
    rethrow;
  }
}
