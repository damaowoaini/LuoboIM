// Source factory injected identically into the two exact reviewed worker files.
// It receives the existing SQL/VFS adapters; no message body leaves the worker.
function createLuoboOwnReadTimeWorker({ getDB, getFS, active, stamp, onUnconfirmed, reply, errorCode }) {
  const validSeq = value => Number.isSafeInteger(value) && value > 0;
  const validTime = value => validSeq(value) && value >= 946684800000 && value <= 4102444800000;
  const millis = value => validSeq(value) ? value >= 946684800 && value <= 4102444800 ? value*1000 : validTime(value) ? value : undefined : undefined;
  const unavailable = () => { throw new Error('own read time unavailable'); };
  const query = (db, sql, values = []) => {
    const statement = db.prepare(sql);
    try {
      statement.bind(values);
      const rows = [];
      while (statement.step()) rows.push(statement.getAsObject());
      return rows;
    } finally { statement.free(); }
  };
  const execute = (db, sql, values = []) => {
    const statement = db.prepare(sql);
    try { statement.bind(values); statement.step(); } finally { statement.free(); }
  };
  const current = (binding, owner, db) => {
    const now = active();
    return !!now && now === binding && now.owner === owner && now.db === db;
  };
  const tableName = cid => '"chat_logs_' + cid.replace(/"/g, '""') + '"';
  async function capture(owner, dataDir) {
    try {
      const binding = active(), db = await getDB();
      if (!binding || binding.owner !== owner || binding.dataDir !== dataDir || !current(binding, owner, db)) unavailable();
      return reply(binding.token);
    } catch { return reply(undefined, errorCode, 'own read time unavailable'); }
  }
  async function scope(cid,token,owner) {
    try {
      const binding=active();
      if (!binding||binding.token!==token||binding.owner!==owner||typeof cid!=='string'||!cid) unavailable();
      const captured=stamp(binding,cid),db=await getDB();
      if (!current(binding,owner,db)||captured!==stamp(binding,cid)) unavailable();
      return reply(captured);
    } catch {return reply(undefined,errorCode,'own read scope unavailable');}
  }
  async function apply(cid, encoded, token, owner) {
    let begun = false, committed = false, durable = false, db, binding;
    try {
      binding = active();
      db = await getDB();
      if (!binding || !current(binding, owner, db) || binding.token !== token || typeof cid !== 'string' || !cid) unavailable();
      const request = JSON.parse(encoded);
      if (!request || request.version !== 1 || request.ownerUserID !== owner || request.conversationID !== cid ||
          request.sessionType !== 1 || typeof request.peerUserID !== 'string' || !request.peerUserID ||
          request.peerUserID === owner || typeof request.receiptID !== 'string' || !request.receiptID ||
          !validTime(request.readTime) || request.readScope !== stamp(binding,cid) || !Array.isArray(request.rows) || !request.rows.length) unavailable();
      const uniqueIDs = new Set(), uniqueSeqs = new Set();
      for (const row of request.rows) {
        if (!row || typeof row.clientMsgID !== 'string' || !row.clientMsgID || !validSeq(row.seq) ||
            row.sendID !== request.peerUserID || row.recvID !== owner || uniqueIDs.has(row.clientMsgID) || uniqueSeqs.has(row.seq)) unavailable();
        uniqueIDs.add(row.clientMsgID); uniqueSeqs.add(row.seq);
      }
      // No await between the final binding check and the complete SQL TX.
      if (!current(binding, owner, db)) unavailable();
      db.exec('BEGIN IMMEDIATE'); begun = true;
      const conversation = query(db, 'SELECT conversation_type,user_id FROM local_conversations WHERE conversation_id = ?', [cid]);
      if (conversation.length !== 1 || conversation[0].conversation_type !== 1 || conversation[0].user_id !== request.peerUserID) unavailable();
      db.exec(`CREATE TABLE IF NOT EXISTS local_own_read_time_receipts (
        owner_user_id TEXT NOT NULL, conversation_id TEXT NOT NULL, client_msg_id TEXT NOT NULL,
        seq INTEGER NOT NULL, peer_user_id TEXT NOT NULL, receipt_id TEXT NOT NULL, read_time INTEGER NOT NULL,
        PRIMARY KEY(owner_user_id,conversation_id,client_msg_id,seq))`);
      const returned = [];
      for (const row of request.rows) {
        const messages = query(db, `SELECT client_msg_id,seq,send_id,recv_id,session_type,attached_info FROM ${tableName(cid)} WHERE client_msg_id = ? AND seq = ?`, [row.clientMsgID,row.seq]);
        if (messages.length !== 1) unavailable();
        const message = messages[0];
        if (message.send_id !== row.sendID || message.recv_id !== row.recvID || message.session_type !== 1) unavailable();
        const parsedAttached = message.attached_info == null || message.attached_info === '' ? {} : JSON.parse(message.attached_info);
        const attached = parsedAttached === null ? {} : parsedAttached;
        if (!attached || typeof attached !== 'object' || Array.isArray(attached) ||
            attached.hasReadTime != null && attached.hasReadTime !== 0 && !millis(attached.hasReadTime)) unavailable();
        const previous = query(db, 'SELECT peer_user_id,receipt_id,read_time FROM local_own_read_time_receipts WHERE owner_user_id=? AND conversation_id=? AND client_msg_id=? AND seq=?', [owner,cid,row.clientMsgID,row.seq]);
        if (previous.length > 1 || previous.length && (previous[0].peer_user_id !== request.peerUserID || !validTime(previous[0].read_time))) unavailable();
        const readTime = Math.min(request.readTime, previous.length ? previous[0].read_time : request.readTime);
        const receiptID = previous.length && previous[0].read_time <= request.readTime ? previous[0].receipt_id : request.receiptID;
        // A pre-existing earlier attached deadline remains conservative. The
        // separate receipt row stores only actual server-notification evidence.
        attached.hasReadTime = millis(attached.hasReadTime) ? Math.min(millis(attached.hasReadTime),readTime) : readTime;
        execute(db, `UPDATE ${tableName(cid)} SET is_read=1,attached_info=? WHERE client_msg_id=? AND seq=? AND send_id=? AND recv_id=? AND session_type=1`, [JSON.stringify(attached),row.clientMsgID,row.seq,row.sendID,row.recvID]);
        if (db.getRowsModified() !== 1) unavailable();
        execute(db, 'INSERT INTO local_own_read_time_receipts(owner_user_id,conversation_id,client_msg_id,seq,peer_user_id,receipt_id,read_time) VALUES(?,?,?,?,?,?,?) ON CONFLICT(owner_user_id,conversation_id,client_msg_id,seq) DO UPDATE SET receipt_id=excluded.receipt_id,read_time=excluded.read_time', [owner,cid,row.clientMsgID,row.seq,request.peerUserID,receiptID,readTime]);
        returned.push({...row,readTime:attached.hasReadTime});
      }
      db.exec('COMMIT'); committed = true; begun = false;
      // Existing SAB unlock waits for its IDB transaction. Fallback must await
      // its real oncomplete promise, not just the in-memory SQL COMMIT.
      const fs = getFS(), stream = fs.open(binding.filename,'a+');
      try {
        const file = stream.node.contents;
        file.fsync();
        if (typeof file.ops.luoboAwaitDurable === 'function') await file.ops.luoboAwaitDurable();
        else if (!file.ops.reader || !file.ops.writer) unavailable();
      } finally { fs.close(stream); }
      durable = true;
      if (!current(binding, owner, db) || request.readScope !== stamp(binding,cid)) unavailable();
      return reply({...request,rows:returned});
    } catch {
      if (begun && !committed && db) { try { db.exec('ROLLBACK'); } catch {} }
      if (committed && !durable) onUnconfirmed(db, binding);
      // Only actual persistence failure fences the captured DB. A durable old
      // result after scope change emits no event and cannot fence a new DB.
      // A post-COMMIT failure is never "rolled back".
      // No own-read event is emitted; a genuine replay or inspect may recover.
      return reply(undefined,errorCode,'own read time unavailable');
    }
  }
  return {capture,scope,apply,...luoboOwnReadMetadataTools(query,execute,tableName,millis)};
}
