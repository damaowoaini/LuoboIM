// These helpers operate only on the new metadata table and existing exact rows.
// A delete never manufactures a row; a normal full-row refresh cannot erase a
// confirmed earlier reading time. Existing clear wire semantics are unchanged.
function luoboOwnReadMetadataTools(query, execute, tableName, millis) {
  const own = (value,key) => Object.prototype.hasOwnProperty.call(value,key);
  const exists = db => query(db,"SELECT name FROM sqlite_master WHERE type='table' AND name='local_own_read_time_receipts'").length === 1;
  const fail = () => {throw new Error('own read metadata unavailable');};
  function deleteRows(db,cid,ids,remove) {
    if (!exists(db)) return remove();
    db.exec('BEGIN IMMEDIATE');
    try {
      const result=remove();
      if (ids == null) execute(db,'DELETE FROM local_own_read_time_receipts WHERE conversation_id=?',[cid]);
      else {
        if (!Array.isArray(ids) || ids.some(id=>typeof id!=='string'||!id)) fail();
        for (const id of ids) execute(db,'DELETE FROM local_own_read_time_receipts WHERE conversation_id=? AND client_msg_id=?',[cid,id]);
      }
      db.exec('COMMIT');return result;
    } catch (error) {try{db.exec('ROLLBACK');}catch{}throw error;}
  }
  function preserve(db,cid,field,key,patch) {
    const attachedKey=own(patch,'attached_info')?'attached_info':own(patch,'attachedInfo')?'attachedInfo':undefined;
    if (!exists(db)) return patch;
    if (field!=='client_msg_id' && field!=='seq') fail();
    const rows=query(db,`SELECT client_msg_id,seq,send_id,recv_id,session_type,attached_info FROM ${tableName(cid)} WHERE ${field}=?`,[key]);
    if (!rows.length) return patch;
    if (rows.length!==1) fail();
    const row=rows[0];
    const receipts=query(db,'SELECT owner_user_id,peer_user_id,read_time FROM local_own_read_time_receipts WHERE conversation_id=? AND client_msg_id=? AND seq=?',[cid,row.client_msg_id,row.seq]);
    if (!receipts.length) return patch;
    if (receipts.length!==1 || receipts[0].owner_user_id!==row.recv_id || receipts[0].peer_user_id!==row.send_id || row.session_type!==1 || !millis(receipts[0].read_time)) fail();
    for (const [name,expected] of [['send_id',row.send_id],['recv_id',row.recv_id],['seq',row.seq],['session_type',1],['client_msg_id',row.client_msg_id]]) {
      if (own(patch,name) && patch[name]!==expected) fail();
    }
    if (!attachedKey) return patch;
    const parsedValue=patch[attachedKey]==null||patch[attachedKey]===''?{}:JSON.parse(patch[attachedKey]);
    const parsedPrevious=row.attached_info==null||row.attached_info===''?{}:JSON.parse(row.attached_info);
    const value=parsedValue===null?{}:parsedValue,previous=parsedPrevious===null?{}:parsedPrevious;
    if (!value || typeof value!=='object' || Array.isArray(value) || !previous || typeof previous!=='object' || Array.isArray(previous)) fail();
    const times=[receipts[0].read_time];
    for (const candidate of [value.hasReadTime,previous.hasReadTime]) {
      if (candidate==null||candidate===0) continue;
      const time=millis(candidate);if(!time)fail();times.push(time);
    }
    return {...patch,[attachedKey]:JSON.stringify({...value,hasReadTime:Math.min(...times)})};
  }
  return {deleteRows,preserve};
}
