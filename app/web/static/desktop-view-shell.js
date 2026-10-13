(() => {
 const dataElement=document.getElementById('desktop-shell-data');
 const data=JSON.parse(dataElement.textContent);
 dataElement.remove();
 const frame=document.getElementById('desktop-frame');
 const pulseBody=JSON.stringify({id:data.id,key:data.key,instance:data.instance});
 let pulseRunning=false,pulseStopped=false;
 const heartbeat=async()=>{
  if(pulseRunning||pulseStopped)return;
  pulseRunning=true;
  const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),8000);
  try{
   const response=await fetch('/desktop-session/heartbeat',{method:'POST',headers:{'Content-Type':'text/plain'},body:pulseBody,credentials:'omit',signal:controller.signal});
   if(response.status===403||response.status===409){pulseStopped=true;clearInterval(pulseTimer);frame.remove();document.body.textContent='桌面会话已结束或入口已重新打开，请从云机管理重新打开。';}
  }catch(_){}finally{clearTimeout(timeout);pulseRunning=false;}
 };
 const closePulse=()=>{
  pulseStopped=true;clearInterval(pulseTimer);
  if(!navigator.sendBeacon?.('/desktop-session/close',pulseBody))void fetch('/desktop-session/close',{method:'POST',headers:{'Content-Type':'text/plain'},body:pulseBody,credentials:'omit',keepalive:true}).catch(()=>{});
 };
 const pulseTimer=setInterval(heartbeat,15000);
 window.addEventListener('pagehide',closePulse);
 window.addEventListener('pageshow',()=>{if(pulseStopped){pulseStopped=false;location.reload();}else void heartbeat();});
 window.addEventListener('focus',()=>void heartbeat());
 document.addEventListener('visibilitychange',()=>{if(!document.hidden)void heartbeat();});
 void heartbeat();
 // Give refresh a normal same-origin GET entry instead of resubmitting the handoff form.
 history.pushState(null,'',location.pathname);
 let lastSnapshot='[]',reloadCount=0;
 window.addEventListener('message',event=>{
  if(event.source!==frame.contentWindow||event.origin!=='null'||event.data?.id!==data.id)return;
  if(event.data.type==='desktop-failure'){
   closePulse();
   frame.remove();document.body.textContent=String(event.data.message||'桌面连接失败，请重新打开。').slice(0,300);return;
  }
  if(event.data.type!=='desktop-storage')return;
  const entries=event.data.entries;
  if(!Array.isArray(entries)||entries.length>200||entries.some(entry=>!Array.isArray(entry)||entry.length!==2||entry.some(value=>typeof value!=='string')))return;
  const snapshot=JSON.stringify(entries).replace(/</g,'\\u003c');
  if(snapshot.length>1024*1024||snapshot===lastSnapshot)return;
  lastSnapshot=snapshot;
  if(++reloadCount>5){closePulse();frame.remove();document.body.textContent='桌面连接连续重新加载，请关闭后重新打开，并检查浏览器控制台中的连接错误。';return;}
  frame.srcdoc=data.html.replace('window.__DESKTOP_STORAGE_SNAPSHOT__=[];',`window.__DESKTOP_STORAGE_SNAPSHOT__=${snapshot};`);
 });
 frame.srcdoc=data.html;
})();
