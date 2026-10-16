import 'booking_contract.dart';
import 'booking_pending_journal.dart';

abstract interface class BookingTransport {
  Future<Object?> post(String relativePath, Map<String, Object?> body);
}

final class BookingPage {
  const BookingPage(this.records, this.hasMore, this.nextCursor);
  final List<BookingRecord> records;
  final bool hasMore;
  final String? nextCursor;
}

/// Owns one immutable transport/session and one account-scoped journal handle.
/// Never substitute a new UUID, retarget transport, or invoke SDK Create/Join.
final class MeetingBookingRepository {
  MeetingBookingRepository({required this.transport, required this.journal});
  final BookingTransport transport;
  final BookingPendingJournal journal;
  BookingJournalScope get scope => journal.scope;
  static const _root =
      'signal/meeting_booking/'; // ApiClient rejects leading '/'.

  Future<BookingReceipt> submit(BookingIntent intent) async {
    scope.check();
    final prepared = await journal.prepare(
      intent,
    ); // durable FULL transaction first
    scope.check();
    if (prepared.entry.terminal) {
      return prepared.entry.receipt!;
    }
    if (!prepared.inserted) {
      throw BookingUnconfirmed(intent.id);
    }
    final ticket = await journal.claim(
      intent.id,
      'submit',
      requirePrepared: true,
    );
    return _dispatch(ticket);
  }

  Future<BookingReceipt> _dispatch(BookingJournalTicket ticket) async {
    final intent = ticket.entry.intent;
    if (intent == null) {
      throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
    }
    return _request(
      ticket,
      '$_root${intent.operation.name}',
      intent.payload,
      write: true,
    );
  }

  Future<BookingReceipt> _request(
    BookingJournalTicket ticket,
    String path,
    Map<String, Object?> body, {
    bool write = false,
  }) async {
    try {
      scope.check();
      final raw = await transport.post(path, body);
      scope.check();
      final receipt = BookingReceipt.wire(ticket.entry.id, scope.owner, raw);
      receipt.verifyIntent(ticket.entry.operation, ticket.entry.target);
      if (write && (!receipt.found || !receipt.applied)) {
        throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
      }
      if (receipt.found) {
        await journal.confirm(ticket, receipt);
      } else {
        await journal.release(ticket);
      }
      scope.check();
      return receipt;
    } catch (_) {
      // Any dispatched failure, malformed result, or local commit failure is
      // unknown. No local deletion and no inference from HTTP/server text.
      scope.check();
      try {
        await journal.release(ticket);
      } on BookingLocalFailure catch (_) {
        // A failed CAS never clears a newer claim/receipt. Keep it for recovery.
      }
      scope.check();
      throw BookingUnconfirmed(ticket.entry.id);
    }
  }

  Future<BookingReceipt?> _terminal(String id) async {
    scope.check();
    final entry = await journal.read(id);
    if (entry == null) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    return entry.receipt;
  }

  /// Recovery for prepared AND inflight rows after process restart. Always ask
  /// the original UUID; never automatically resend a write during startup.
  Future<BookingReceipt> recover(String id) async {
    final known = await _terminal(id);
    if (known != null) {
      return known;
    }
    final ticket = await journal.claim(id, 'receipt');
    return _request(ticket, '${_root}receipt', {'clientRequestID': id});
  }

  /// An explicit user action. Receipt-first and the exact same persisted bytes
  /// are mandatory, including when the process originally died before dispatch.
  Future<BookingReceipt> retryFrozen(String id) async {
    final known = await recover(id);
    scope.check();
    if (known.found) {
      return known;
    }
    final terminal = await _terminal(id);
    if (terminal != null) {
      return terminal;
    }
    final ticket = await journal.claim(id, 'retry');
    return _dispatch(ticket);
  }

  /// Resolve only determines the original intent's outcome. An applied Create
  /// remains a scheduled booking; cancelling that booking is a separate intent.
  /// Never call a missing/failed resolve response a successful cancellation.
  Future<BookingReceipt> resolveUnknown(String id) async {
    final known = await _terminal(id);
    if (known != null) {
      return known;
    }
    final ticket = await journal.claim(id, 'resolve');
    final result = await _request(ticket, '${_root}resolve', {
      'clientRequestID': id,
    });
    if (!result.found) {
      throw BookingUnconfirmed(id);
    }
    return result;
  }

  Future<List<BookingReceipt>> recoverPending() async {
    final pending = await journal.pending();
    final results = <BookingReceipt>[];
    for (final entry in pending) {
      scope.check();
      results.add(await recover(entry.id));
    }
    return List.unmodifiable(results);
  }

  Future<BookingRecord> detail(String id) async {
    if (!isBookingUUID(id)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    scope.check();
    final raw = await transport.post('${_root}detail', {'bookingID': id});
    scope.check();
    final record = BookingRecord.wire(raw, scope.owner);
    if (record.id != id) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    await journal.observe([record]);
    scope.check();
    return record;
  }

  Future<BookingPage> list({
    int pageSize = 40,
    String cursor = '',
    bool includeCancelled = false,
  }) async {
    if (pageSize < 1 || pageSize > 100 || cursor.length > 2048) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    scope.check();
    final raw = bookingMap(
      await transport.post('${_root}list', {
        'pageSize': pageSize,
        'cursor': cursor,
        'includeCancelled': includeCancelled,
      }),
    );
    scope.check();
    final rows = raw['bookings'],
        more = raw['hasMore'],
        next = raw['nextCursor'];
    if (rows is! List<Object?> ||
        rows.length > pageSize ||
        more is! bool ||
        (next != null && next is! String) ||
        (more && (next is! String || next.isEmpty))) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final records = rows
        .map((value) => BookingRecord.wire(value, scope.owner))
        .toList(growable: false);
    if (records.map((row) => row.id).toSet().length != records.length) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    await journal.observe(records);
    scope.check();
    return BookingPage(List.unmodifiable(records), more, next as String?);
  }
}
