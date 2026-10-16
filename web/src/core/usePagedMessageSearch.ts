import { captureMessageAccess, messageHistoryFence, visibleMessages } from "./messagePrivacyRuntime";
import { useMessagePrivacyRevision } from "@/hooks/useMessagePrivacy";
import { useCallback, useEffect, useRef, useState } from "react";
import { sessionCoordinator } from "./session/browser";

export function messageSearchOwner(conversationID?:string) {
  const session=sessionCoordinator.current;
  return JSON.stringify([sessionCoordinator.generation,session?.issuer,session?.tenant.serverID,session?.userID,conversationID]);
}
type Ticket={epoch:number;keyword:string;page:number};
export class SearchPageCursor {
  private epoch=0;
  private keyword="";
  private nextPage=1;
  private busy=false;
  private more=true;
  reset(){this.epoch++;this.keyword="";this.nextPage=1;this.busy=false;this.more=true;}
  begin(keyword:string,append:boolean):Ticket|undefined{
    if(append){if(this.busy||!this.more||keyword!==this.keyword)return;}
    else{this.reset();this.keyword=keyword;}
    this.busy=true;return {epoch:this.epoch,keyword,page:this.nextPage};
  }
  current(ticket:Ticket){return ticket.epoch===this.epoch&&ticket.keyword===this.keyword;}
  accept(ticket:Ticket,count:number,pageSize:number){
    if(!this.current(ticket))return false;
    this.busy=false;this.nextPage=ticket.page+1;this.more=count===pageSize;return true;
  }
  fail(ticket:Ticket){if(!this.current(ticket))return false;this.busy=false;return true;}
}

export function usePagedMessageSearch<T>({conversationID,enabled,requireKeyword=false,loadPage,onError}:{
  conversationID?:string;enabled:boolean;requireKeyword?:boolean;
  loadPage(keyword:string,page:number):Promise<T[]>;onError(error:unknown):void;
}){
  const privacyRevision = useMessagePrivacyRevision();
  const historyRevision = messageHistoryFence.version();
  const owner=messageSearchOwner(conversationID)+":"+historyRevision;
  const lastHistoryRevision = useRef(historyRevision), needsExplicitRefresh = useRef(false), previousEnabled=useRef(enabled);
  if(lastHistoryRevision.current!==historyRevision){lastHistoryRevision.current=historyRevision;needsExplicitRefresh.current=true;}
  if(previousEnabled.current!==enabled){previousEnabled.current=enabled;if(enabled)needsExplicitRefresh.current=false;}
  const latest=useRef({owner,enabled,loadPage,onError});latest.current={owner,enabled,loadPage,onError};
  const mounted=useRef(false),cursor=useRef(new SearchPageCursor());
  const [state,setState]=useState({loading:false,messageList:[] as T[]});
  useEffect(()=>{
    mounted.current=true;cursor.current.reset();setState({loading:false,messageList:[]});
    return()=>{mounted.current=false;cursor.current.reset();};
  },[owner,enabled]);
  const current=()=>mounted.current&&enabled&&latest.current.enabled&&latest.current.owner===owner&&(messageSearchOwner(conversationID)+":"+messageHistoryFence.version())===owner;
  const search=useCallback((keyword:string,append=false,automatic=false)=>{
    if(!current()||!conversationID||automatic&&needsExplicitRefresh.current)return;
    if(!automatic)needsExplicitRefresh.current=false;
    if(requireKeyword&&!keyword){cursor.current.reset();setState({loading:false,messageList:[]});return;}
    const ticket=cursor.current.begin(keyword,append);if(!ticket)return;
    const access=captureMessageAccess(conversationID);
    const load=latest.current.loadPage;
    setState(previous=>({loading:true,messageList:append?previous.messageList:[]}));
    Promise.resolve().then(()=>{if(!current()||!access.current()||!cursor.current.current(ticket))return undefined;return load(keyword,ticket.page);}).then(messages=>{
      if(messages===undefined||!current()||!access.current()||!cursor.current.accept(ticket,messages.length,20))return;
      setState(previous=>({loading:false,messageList:[...(append?previous.messageList:[]),...messages]}));
    }).catch(error=>{
      if(!current()||!cursor.current.fail(ticket))return;
      setState(previous=>({...previous,loading:false}));latest.current.onError(error);
    });
  },[owner,enabled,conversationID,requireKeyword]);
  const updateMessages=useCallback((update:(messages:T[])=>T[])=>{
    if(current())setState(previous=>({...previous,messageList:update(previous.messageList)}));
  },[owner,enabled,conversationID]);
  // Refilter already-rendered results when a known deadline or deletion tombstone changes.
  void privacyRevision;
  return {...state,messageList:visibleMessages(state.messageList,conversationID),search,updateMessages};
}
