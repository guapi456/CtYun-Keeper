(() => {
 const bootstrap=window.__DESKTOP_BOOTSTRAP__;
 delete window.__DESKTOP_BOOTSTRAP__;
 if(!bootstrap)return;
 const origin=bootstrap.origin||new URL(location.href).origin;
 const assetBase=`${origin}/desktop-assets/pc/`;
 window.__ASSET_PUBLIC_PATH__=assetBase;
 const pointsFrames=new Set();
 if(typeof HTMLIFrameElement!=='undefined'){
  const frameSource=Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype,'src');
  const mapFrame=(frame,value)=>{
   const url=new URL(String(value),origin);
   if(url.origin==='https://desk.ctyun.cn'&&url.pathname==='/selforder/points.html'){
    pointsFrames.add(frame);return `${origin}/desktop-points?deviceType=60`;
   }
   return value;
  };
  Object.defineProperty(HTMLIFrameElement.prototype,'src',{...frameSource,set(value){frameSource.set.call(this,mapFrame(this,value));}});
  const setAttribute=HTMLIFrameElement.prototype.setAttribute;
  HTMLIFrameElement.prototype.setAttribute=function(name,value){return setAttribute.call(this,name,String(name).toLowerCase()==='src'?mapFrame(this,value):value);};
  window.addEventListener('message',event=>{
   if(event.origin!=='null'||event.data?.type!=='desktop-points-ready')return;
   for(const frame of pointsFrames){if(frame.contentWindow===event.source){event.source.postMessage({type:'desktop-points-relay',id:bootstrap.id,key:bootstrap.relayKey,origin},'*');break;}}
  });
 }
 // Statistics scripts are intentionally excluded; vendor error reporting must remain harmless.
 window.ctct={track:()=>{}};
 // Use the client's existing TLS WebSocket path rather than QUIC/WebTransport.
 Object.defineProperty(window,'WebTransport',{value:undefined});
 // The official bundle checks this property even when registration is unsupported.
 Object.defineProperty(navigator,'serviceWorker',{value:{register:()=>Promise.reject(new Error('桌面隔离窗口不支持 Service Worker'))}});
 let loadError=false;
 const showStatus=()=>{
  if(!document.body)return;
  let panel=document.getElementById('desktop-load-status');
  if(!loadError&&document.getElementById('app')?.childElementCount){panel?.remove();return;}
  if(!panel){panel=document.createElement('div');panel.id='desktop-load-status';panel.style.cssText='position:fixed;z-index:2147483647;top:16px;left:16px;right:16px;padding:16px;border:1px solid #d1d5db;border-radius:8px;background:#fff;color:#1f2937;font:14px sans-serif';document.body.append(panel);}
  const message=loadError?'桌面页面加载失败，请关闭后重新打开；若仍失败，请查看浏览器控制台。':'正在加载桌面页面…';
  if(panel.textContent!==message)panel.textContent=message;
 };
 window.addEventListener('error',event=>{if(/(?:chrome|moz)-extension:|\.user\.js/.test(event.filename||''))return;if(event.target?.tagName&&event.target.tagName!=='SCRIPT')return;loadError=true;showStatus();},true);
 document.addEventListener('DOMContentLoaded',()=>{showStatus();new MutationObserver(showStatus).observe(document.getElementById('app')||document.body,{childList:true,subtree:true});},{once:true});
 const snapshot=window.__DESKTOP_STORAGE_SNAPSHOT__;
 delete window.__DESKTOP_STORAGE_SNAPSHOT__;
 const memory=new Map(Array.isArray(snapshot)?snapshot:[]);
 const storage=new Proxy({
  getItem:key=>memory.has(String(key))?memory.get(String(key)):null,
  setItem:(key,value)=>memory.set(String(key),String(value)),
  removeItem:key=>memory.delete(String(key)),clear:()=>memory.clear(),
  key:index=>[...memory.keys()][index]??null,
  get length(){return memory.size;},
 },{get:(target,key)=>key in target?target[key]:memory.get(String(key)),set:(target,key,value)=>{memory.set(String(key),String(value));return true;}});
 // Opaque sandbox documents cannot use real browser storage. Keep each window isolated.
 Object.defineProperty(window,'localStorage',{value:storage});
 Object.defineProperty(window,'sessionStorage',{value:storage});
 // Vendor cookie probes must not touch management cookies or throw in an opaque frame.
 Object.defineProperty(document,'cookie',{get:()=>'',set:()=>{}});
 // Vue Router otherwise catches opaque-origin history errors and navigates to the management root.
 let routeState=null;
 Object.defineProperty(history,'state',{get:()=>routeState});
 const changeRoute=(state,_title,value)=>{
  routeState=state;
  if(value==null)return;
  const route=new URL(String(value),`${origin}/`);
  if(route.origin!==origin)throw new Error('不支持的桌面路由');
  if(route.hash&&route.hash!==location.hash)location.hash=route.hash;
 };
 history.replaceState=changeRoute;
 history.pushState=changeRoute;
 if(!storage.getItem('authData'))storage.setItem('authData',JSON.stringify(bootstrap.auth));
 storage.setItem('web_device_code',bootstrap.deviceCode);
 storage.setItem('authExpiredAt',bootstrap.expires);
 storage.setItem('judgeUserEId',bootstrap.auth.userEid||'');
 storage.setItem('loginAt',Date.now());
 storage.setItem('user_name',bootstrap.auth.userName||'');
 storage.setItem('userId',bootstrap.auth.userId);
 delete bootstrap.auth;
 delete bootstrap.deviceCode;
 document.querySelector('script[data-desktop-bootstrap]')?.remove();
 try{history.replaceState(null,'',`#${bootstrap.hash}`);}catch(_){location.hash=bootstrap.hash;}

 const bannerPath='/cloudDesktopServer/ctyundesk/operation/banner';
 const isAPI=url=>url.protocol==='https:'&&(url.hostname==='desk.ctyun.cn'||/^[a-z0-9-]+-deskmgr\.ctyun\.cn$/.test(url.hostname))&&['8810','8816'].includes(url.port)&&(url.pathname.startsWith('/api/')||url.pathname===bannerPath);
 const resource=value=>{
  if(String(value).startsWith('blob:')||String(value).startsWith('data:'))return String(value);
  if(String(value).startsWith('/desktop-assets/'))return new URL(value,origin).href;
  const u=new URL(value,'https://pc.ctyun.cn/');
  if(u.origin===origin&&u.pathname.startsWith('/desktop-assets/'))return u.href;
  if(u.hostname==='pc.ctyun.cn')return `${assetBase}${u.pathname.replace(/^\//,'')}${u.search}`;
  if(u.hostname==='deskcdn.ctyun.cn')return `${origin}/desktop-assets/cdn/${u.pathname.replace(/^\//,'')}${u.search}`;
  throw new Error('不支持的桌面资源');
 };
 const destination=value=>{
  const u=new URL(value,/^\/?(?:api|cloudDesktopServer)\//.test(String(value))?'https://desk.ctyun.cn:8810/':'https://pc.ctyun.cn/');
  if(isAPI(u))return{url:`${origin}/desktop-relay?target=${encodeURIComponent(u.href)}`,api:true};
  return{url:resource(value),api:false};
 };
 const nativeFetch=window.fetch.bind(window);
 const optionalQueries=new Set([
  '/api/appvirtual/client/category/list',
  '/api/appvirtual/client/pageList',
  '/api/appvirtual/client/genConnectToken',
  bannerPath,
  '/api/cdserv/client/msgcenter/page',
  '/api/cdserv/client/msgcenter/thirdPartyData/saveTemporary',
  '/api/cdserv/client/getSoftwareIcon',
  '/api/auth/client/listUserProperties',
  '/api/auth/businessGroup/checkIfObjsCanJoinGroup',
  '/api/auth/businessGroup/queryBusinessGroup',
  '/api/auth/businessGroup/queryCombineDataList',
  '/api/auth/businessGroup/queryRedDotNum',
  '/api/cdserv/client/dataservice/api/dataEvent/sendBatch',
 ]);
 const relayFailure=(reason,input)=>{
  const path=new URL(input,origin).pathname;
  if(reason==='unsupported-request'&&optionalQueries.has(path)){console.warn(`桌面辅助请求未支持：${path}`);return;}
  const message=reason==='invalid-session'?'桌面会话已失效，请从云机管理重新打开。':`官方客户端请求尚未支持：${path}。请将此提示反馈以补齐接口。`;
  window.parent.postMessage({type:'desktop-failure',id:bootstrap.id,message},origin);
 };
 window.fetch=(input,init={})=>{
  const source=input instanceof Request?input:undefined;
  const next=destination(source?source.url:String(input));
  const headers=new Headers(init.headers||source?.headers);
  if(next.api){headers.set('X-Desktop-Session',bootstrap.id);headers.set('X-Desktop-Key',bootstrap.relayKey);}
  const target=source?new Request(next.url,source):next.url;
  return nativeFetch(target,{...init,headers,credentials:'omit',redirect:'error'}).then(response=>{const reason=response.headers?.get('X-Desktop-Relay-Error');if(next.api&&reason)relayFailure(reason,source?source.url:String(input));return response;});
 };
 const open=XMLHttpRequest.prototype.open,send=XMLHttpRequest.prototype.send;
 XMLHttpRequest.prototype.open=function(method,url,...args){const next=destination(String(url));this.__desktopAPI=next.api;this.__desktopOriginal=String(url);return open.call(this,method,next.url,...args);};
 XMLHttpRequest.prototype.send=function(body){this.withCredentials=false;if(this.__desktopAPI){this.setRequestHeader('X-Desktop-Session',bootstrap.id);this.setRequestHeader('X-Desktop-Key',bootstrap.relayKey);this.addEventListener?.('load',()=>{const reason=this.getResponseHeader('X-Desktop-Relay-Error');if(reason)relayFailure(reason,this.__desktopOriginal);},{once:true});}return send.call(this,body);};

 const NativeWorker=window.Worker;
 if(NativeWorker){window.Worker=function(value,options){
  if(String(value).startsWith('blob:'))return new NativeWorker(value,options);
  let source=String(value);
  if(source.startsWith(`${location.pathname}workers/`))source=source.slice(location.pathname.length);
  const asset=resource(source);
  const prelude=`const base=${JSON.stringify(asset)};const resolve=value=>new URL(String(value),base).href;const nativeFetch=self.fetch.bind(self);self.fetch=(input,init={})=>nativeFetch(typeof input==='string'||input instanceof URL?resolve(input):input,{...init,credentials:'omit'});const nativeOpen=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(method,url,...args){return nativeOpen.call(this,method,resolve(url),...args);};`;
  const script=prelude+(options?.type==='module'?`import(${JSON.stringify(asset)});`:`importScripts(${JSON.stringify(asset)});`);
  const blob=URL.createObjectURL(new Blob([script],{type:'application/javascript'}));
  return new NativeWorker(blob,options);
 };window.Worker.prototype=NativeWorker.prototype;}
 window.addEventListener('pagehide',()=>{
  if(window.parent!==window)window.parent.postMessage({type:'desktop-storage',id:bootstrap.id,entries:[...memory.entries()]},origin);
  memory.clear();
 },{once:true});
 setTimeout(()=>{memory.clear();loadError=true;showStatus();const panel=document.getElementById('desktop-load-status');if(panel)panel.textContent='桌面会话已过期，请关闭窗口后从云机管理重新打开。';},Math.max(0,bootstrap.expires-Date.now()));
})();
