(() => {
 const memory=new Map();
 const storage=new Proxy({getItem:key=>memory.get(String(key))??null,setItem:(key,value)=>memory.set(String(key),String(value)),removeItem:key=>memory.delete(String(key)),clear:()=>memory.clear(),key:index=>[...memory.keys()][index]??null,get length(){return memory.size;}},{get:(target,key)=>key in target?target[key]:memory.get(String(key)),set:(target,key,value)=>{memory.set(String(key),String(value));return true;}});
 Object.defineProperty(window,'localStorage',{value:storage});
 Object.defineProperty(window,'sessionStorage',{value:storage});
 Object.defineProperty(document,'cookie',{get:()=>'',set:()=>{}});
 let resolveRelay,relay;
 const ready=new Promise(resolve=>{resolveRelay=resolve;});
 window.addEventListener('message',event=>{
  const data=event.data;
  if(event.source!==window.parent||event.origin!=='null'||data?.type!=='desktop-points-relay'||relay)return;
  if(typeof data.id!=='string'||typeof data.key!=='string'||typeof data.origin!=='string')return;
  relay=data;resolveRelay(data);
 });
 window.parent.postMessage({type:'desktop-points-ready'},'*');
 const apiURL=value=>{
  const url=new URL(String(value),'https://desk.ctyun.cn/selforder/points.html');
  if(!url.pathname.startsWith('/selforder/api/'))return null;
  if(url.hostname===location.hostname)url.href='https://desk.ctyun.cn'+url.pathname+url.search;
  return url;
 };
 const nativeFetch=window.fetch.bind(window);
 window.fetch=async(input,init={})=>{
  const original=input instanceof Request?input.url:String(input),target=apiURL(original);
  if(!target)return nativeFetch(input,init);
  const data=await ready,headers=new Headers(init.headers||(input instanceof Request?input.headers:undefined));
  headers.set('X-Desktop-Session',data.id);headers.set('X-Desktop-Key',data.key);
  const address=`${data.origin}/desktop-relay?target=${encodeURIComponent(target.href)}`;
  return nativeFetch(input instanceof Request?new Request(address,input):address,{...init,headers,credentials:'omit'});
 };
 const open=XMLHttpRequest.prototype.open,send=XMLHttpRequest.prototype.send,setHeader=XMLHttpRequest.prototype.setRequestHeader;
 XMLHttpRequest.prototype.open=function(method,url,...args){this.__pointsTarget=apiURL(String(url));this.__pointsMethod=method;this.__pointsArgs=args;this.__pointsHeaders=[];this.__pointsAborted=false;return open.call(this,method,this.__pointsTarget?'/desktop-relay':url,...args);};
 XMLHttpRequest.prototype.setRequestHeader=function(name,value){if(this.__pointsTarget)this.__pointsHeaders.push([name,value]);else return setHeader.call(this,name,value);};
 XMLHttpRequest.prototype.send=function(body){if(!this.__pointsTarget)return send.call(this,body);const responseType=this.responseType,timeout=this.timeout;void ready.then(data=>{if(this.__pointsAborted)return;open.call(this,this.__pointsMethod,`${data.origin}/desktop-relay?target=${encodeURIComponent(this.__pointsTarget.href)}`,...this.__pointsArgs);this.responseType=responseType;this.timeout=timeout;this.withCredentials=false;for(const[name,value]of this.__pointsHeaders)setHeader.call(this,name,value);setHeader.call(this,'X-Desktop-Session',data.id);setHeader.call(this,'X-Desktop-Key',data.key);send.call(this,body);});};
 const abort=XMLHttpRequest.prototype.abort;
 XMLHttpRequest.prototype.abort=function(){this.__pointsAborted=true;return abort.call(this);};
})();
