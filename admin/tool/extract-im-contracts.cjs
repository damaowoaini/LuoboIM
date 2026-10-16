// Read-only AST inventory. Writes only the structured current contract catalog.
const fs=require('node:fs'),path=require('node:path'),parser=require('@babel/parser');
const root=path.resolve(__dirname,'..'),target=path.resolve(root,'../docs/architecture/admin-im-capability-contracts.json');
const result=JSON.parse(fs.readFileSync(target,'utf8'));const endpoints=[];
function visit(node,source,file,symbol=''){
  if(!node||typeof node!=='object')return;
  if(node.type==='FunctionDeclaration')symbol=node.id?.name||symbol;
  if(node.type==='VariableDeclarator'&&node.id?.type==='Identifier')symbol=node.id.name;
  if(node.type==='CallExpression'&&node.callee?.name==='request'){
    const [url,options]=node.arguments;
    const properties=options?.type==='ObjectExpression'?options.properties:[];
    const property=name=>properties.find(p=>p.type==='ObjectProperty'&&(p.key.name||p.key.value)===name)?.value;
    if(property('baseURL')?.name==='API_URL'&&url?.type==='StringLiteral'){
      const data=property('data');endpoints.push({path:url.value,method:property('method')?.value||'GET',symbol,
        source:{path:'admin/'+path.relative(root,file),line:node.loc.start.line},dataExpression:data?source.slice(data.start,data.end):null});
    }
  }
  for(const [key,value]of Object.entries(node))if(key!=='loc')Array.isArray(value)?value.forEach(v=>visit(v,source,file,symbol)):visit(value,source,file,symbol);
}
function files(dir){for(const entry of fs.readdirSync(dir)){const file=path.join(dir,entry);if(fs.statSync(file).isDirectory())files(file);else if(/\.tsx?$/.test(file)){const source=fs.readFileSync(file,'utf8');visit(parser.parse(source,{sourceType:'module',plugins:['typescript',...(file.endsWith('.tsx')?['jsx']:[])]}),source,file);}}}
files(path.join(root,'src/services'));
const types=fs.readFileSync(path.join(root,'src/services/server/typings.d.ts'),'utf8'),schemas={};
function namespaces(node,prefix=[]){if(!node||typeof node!=='object')return;if(node.type==='TSModuleDeclaration')return namespaces(node.body,[...prefix,node.id.name]);if(node.type==='TSTypeAliasDeclaration')schemas[[...prefix,node.id.name].join('.')]=types.slice(node.typeAnnotation.start,node.typeAnnotation.end);for(const [key,value]of Object.entries(node))if(key!=='loc')Array.isArray(value)?value.forEach(v=>namespaces(v,prefix)):namespaces(value,prefix);}
namespaces(parser.parse(types,{sourceType:'module',plugins:['typescript']}));
result.scope='Current admin request AST with baseURL API_URL; fixture prefix /api. Historical31 included legacy search_msg, now moved to audited chart API. No credential values.';
result.endpoints=endpoints;result.parameterSchemas=schemas;result.historyMustMoveToScopedAudit=[];
result.historyMovedToScopedAudit={'legacy':'/api/msg/search_msg','current':'/complete_admin/admin/message_search','status':'frontend source adapted; new binary live acceptance pending'};
result.notes.push('get_users uses only pagination in current server protobuf; legacy keyword/normal/genders were ignored. No IM user search/filter capability is claimed.');
fs.writeFileSync(target,JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({endpoints:endpoints.length,parameterSchemas:Object.keys(schemas).length}));
