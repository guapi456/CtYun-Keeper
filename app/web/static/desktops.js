(() => {
 const root = document.querySelector('[data-cloud-manager]');
 if (!root) return;
 const find = s => document.querySelector(s);
 const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
 const accounts = [...find('[data-cloud-account]').options].filter(o => o.value).map(o => ({id:Number(o.value),name:o.textContent}));
 let desktops = [], page = 1, loading = false, polling = false, holdRevision = 0;
 const selected = new Map();
 const key = d => `${d.account_id}:${d.id}`;
 const labels = {poweron:'开机',shutdown:'关机',reboot:'重启'};
 const statuses = {queued:'排队中',validating:'校验中',submitting:'提交中',accepted:'指令已接受',unknown:'待核对',failed:'失败',cancelled:'已取消'};
 const request = async (url, body, signal) => {
  const response = await fetch(url, body ? {method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(body),signal} : {headers:{'Accept':'application/json'},signal});
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || '请求失败');
  return data;
 };
 const notice = text => {find('[data-cloud-notice]').textContent = text;};
 const filtered = () => {
  const account = find('[data-cloud-account]').value;
  const query = find('[data-cloud-search]').value.trim().toLowerCase();
  return desktops.filter(d => (!account || String(d.account_id)===account) && (!query || `${d.name} ${d.id} ${d.account_name}`.toLowerCase().includes(query)));
 };
 const button = (text, callback) => {const b=document.createElement('button');b.type='button';b.className='cloud-text-button';b.textContent=text;b.addEventListener('click',callback);return b;};
 const iconButton=(label,icon,callback)=>{
  const b=button('',callback);b.className=`icon-button cloud-machine-icon cloud-machine-icon-${icon}`;b.title=label;b.setAttribute('aria-label',label);
  const paths={open:'<path d="M10 5H4a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-6M8 22h8M12 19v3M14 2h8v8M22 2 12 12"/>',resume:'<path d="M6 3v5M12 3v5M3 8h12v3a6 6 0 0 1-12 0V8ZM9 17v4"/><circle cx="18" cy="17" r="5" fill="currentColor" stroke="none"/><path d="m15.5 17 1.7 1.7 3.3-3.4" stroke="#fff" stroke-width="2"/>'};
  b.innerHTML=`<svg width="27" height="27" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[icon]}</svg>`;
  return b;
 };
 const updateSelection = () => {
  find('[data-cloud-selection]').textContent=`已选 ${selected.size} 台`;
  const values=filtered().filter(d=>!d.forbidden),count=values.filter(d=>selected.has(key(d))).length;
  find('[data-cloud-select-all]').checked=values.length>0&&count===values.length;
  find('[data-cloud-select-all]').indeterminate=count>0&&count<values.length;
 };
 const pageSize=12, cardNodes=new Map();
 let detailGeneration=0, detailInFlight=0, detailQueue=[];
 const detailControllers=new Set(),detailActiveAccounts=new Set();
 const visibleDesktops=()=>filtered().slice((page-1)*pageSize,page*pageSize);
 const copyButton=(label,value)=>{
  const b=button('复制',async()=>{try{if(!navigator.clipboard)throw new Error('clipboard');await navigator.clipboard.writeText(String(value));b.textContent='已复制';setTimeout(()=>{if(b.isConnected)b.textContent='复制';},1500);}catch(_){b.textContent='请手动复制';}});
  b.setAttribute('aria-label',`复制${label}`);return b;
 };
 const fillCard = (card,d) => {
  const result=d.details,cfg=result?.configuration;
  const header=card.querySelector('.cloud-machine-head');
  header.querySelector('h2').textContent=d.name;
  header.querySelector('.cloud-machine-account').textContent=d.account_name;
  header.querySelector('input').checked=selected.has(key(d));header.querySelector('input').disabled=d.forbidden||loading;
  const state=header.querySelector('.cloud-machine-state');state.textContent=d.status;state.className=`cloud-machine-state pill ${d.forbidden?'warning':''}`;
  const hold=card.querySelector('.cloud-machine-hold');hold.textContent=d.hold||'';hold.hidden=!d.hold;
  const body=card.querySelector('.cloud-machine-sections');body.replaceChildren();
  const section=heading=>{const section=document.createElement('section');section.className='cloud-machine-section';const h=document.createElement('h3');h.textContent=heading;const dl=document.createElement('dl');section.append(h,dl);body.append(section);return dl;};
  const row=(dl,label,value,copy=false)=>{const div=document.createElement('div');const dt=document.createElement('dt');dt.textContent=label;const dd=document.createElement('dd');const text=document.createElement('span');text.textContent=cfg?display(value):(label==='桌面 ID'?String(value):'—');text.title=text.textContent;dd.append(text);if(copy&&value)dd.append(copyButton(label,value));div.append(dt,dd);dl.append(div);};
  const base=section('基础信息');row(base,'桌面 ID',result?.code||d.id,true);row(base,'类型',cfg?.productName);row(base,'配置',result?.specification);row(base,'系统盘 / 数据盘',cfg?`${amount(cfg.systemDiskGB)} / ${amount(cfg.dataDiskGB)}`:null);
  const image=section('镜像信息');row(image,'当前镜像',cfg?.mirrorCategoryName);row(image,'镜像版本号',cfg?.mirrorVersion,true);row(image,'镜像来源',result?.mirror_source);row(image,'镜像系统',cfg?.os);
  const plan=section('套餐时长');const validCreate=Number.isFinite(cfg?.createDate)&&cfg.createDate>0,validNow=Number.isFinite(cfg?.nowDate)&&cfg.nowDate>0,validExpire=Number.isFinite(cfg?.expireDate)&&cfg.expireDate>0;
  row(plan,'已使用时长',validCreate&&validNow?duration(cfg.nowDate-cfg.createDate):'未提供');row(plan,'剩余时长',validNow&&validExpire?duration(Math.max(0,cfg.expireDate-cfg.nowDate)):'未提供');row(plan,'到期时间',timestamp(cfg?.expireDate));
  const status=card.querySelector('.cloud-machine-config-state');status.replaceChildren();status.classList.toggle('error',d.detailState==='error');
  if(d.detailState==='error'){const message=document.createElement('span');message.textContent=`读取失败：${d.detailError}`;status.append(message,button('重试读取',()=>{d.detailState=undefined;d.detailError='';queueDetails();}));}
  else if(!cfg)status.textContent=d.detailState==='loading'?'正在读取配置…':'配置等待加载…';
  status.hidden=!!cfg;
  const actions=card.querySelector('.cloud-machine-actions');actions.replaceChildren();const open=iconButton('打开桌面','open',()=>openDesktop(d));open.disabled=d.forbidden;actions.append(open);if(d.hold)actions.append(iconButton('恢复连接','resume',()=>resume(d)));
  card.classList.toggle('is-selected',selected.has(key(d)));
 };
 const updateCard=d=>{const card=cardNodes.get(key(d));if(card)fillCard(card,d);};
 const pumpDetails=()=>{
  const visible=new Set(visibleDesktops().map(key));
  detailQueue=detailQueue.filter(d=>{if(visible.has(key(d)))return true;d.detailState=undefined;return false;});
  while(detailInFlight<3&&detailQueue.length){
   const index=detailQueue.findIndex(d=>!detailActiveAccounts.has(d.account_id));if(index<0)break;
   const d=detailQueue.splice(index,1)[0];
   const generation=detailGeneration,abort=new AbortController();detailControllers.add(abort);detailActiveAccounts.add(d.account_id);detailInFlight++;d.detailState='loading';updateCard(d);
   request(`/api/desktops/details?account_id=${d.account_id}&desktop_id=${encodeURIComponent(d.id)}`,undefined,abort.signal).then(result=>{
    if(generation!==detailGeneration)return;d.details=result;d.detailState='ready';updateCard(d);
   }).catch(error=>{if(generation!==detailGeneration)return;if(error.name==='AbortError')d.detailState=undefined;else{d.detailState='error';d.detailError=error.message;}updateCard(d);
   }).finally(()=>{detailControllers.delete(abort);detailActiveAccounts.delete(d.account_id);detailInFlight--;pumpDetails();});
  }
 };
 const queueDetails=()=>{
  if(loading)return;
  for(const d of visibleDesktops()){if(!d.detailState){d.detailState='queued';detailQueue.push(d);}}
  pumpDetails();
 };
 const render = () => {
  const values=filtered(),pages=Math.max(1,Math.ceil(values.length/pageSize));page=Math.min(page,pages);
  const host=find('[data-cloud-cards]');host.replaceChildren();cardNodes.clear();
  for(const d of visibleDesktops()){
   const card=document.createElement('article');card.className='cloud-machine-card';card.setAttribute('aria-label',d.name);
   const head=document.createElement('header');head.className='cloud-machine-head';
   const check=document.createElement('input');check.type='checkbox';check.setAttribute('aria-label',`选择 ${d.name}`);
   check.addEventListener('change',()=>{if(check.checked)selected.set(key(d),{account_id:d.account_id,desktop_id:d.id});else selected.delete(key(d));card.classList.toggle('is-selected',check.checked);updateSelection();});
   const title=document.createElement('div');const h=document.createElement('h2');const small=document.createElement('small');small.className='cloud-machine-account';title.append(h,small);
   const actions=document.createElement('div');actions.className='cloud-machine-actions';
   const state=document.createElement('span');state.className='cloud-machine-state';head.append(check,title,actions,state);
   const hold=document.createElement('p');hold.className='cloud-machine-hold';const body=document.createElement('div');body.className='cloud-machine-sections';const status=document.createElement('div');status.className='cloud-machine-config-state';status.setAttribute('role','status');card.append(head,hold,body,status);host.append(card);cardNodes.set(key(d),card);fillCard(card,d);
  }
  if(!values.length){const empty=document.createElement('p');empty.className='muted';empty.textContent=loading?'正在查询…':'暂无云机';host.append(empty);}
  find('[data-cloud-page]').textContent=`第 ${page} / ${pages} 页 · 共 ${values.length} 台`;
  find('[data-cloud-prev]').disabled=page===1;find('[data-cloud-next]').disabled=page>=pages;updateSelection();queueDetails();
 };
 const refresh = async () => {
  if(loading)return;loading=true;detailGeneration++;for(const abort of detailControllers)abort.abort();detailQueue=[];selected.clear();desktops=[];find('[data-cloud-errors]').replaceChildren();find('[data-cloud-refresh]').disabled=true;render();
  let next=0,done=0;
  const worker=async()=>{while(next<accounts.length){const a=accounts[next++];try{const result=await request(`/api/desktops/list?account_id=${a.id}`);desktops.push(...result.desktops);if(result.warning)addError(a.name,result.warning);}catch(e){addError(a.name,e.message);}done++;notice(`已查询 ${done} / ${accounts.length} 个账号，共 ${desktops.length} 台云机`);render();}};
  await Promise.all(Array.from({length:Math.min(3,accounts.length)},worker));loading=false;find('[data-cloud-refresh]').disabled=false;render();
  if(!accounts.length)notice('请先添加账号');
 };
 const addError=(name,message)=>{const p=document.createElement('p');p.textContent=`${name}：${message}`;find('[data-cloud-errors]').append(p);};
 const display = value => value===null||value===undefined||value===''?'未提供':String(value);
 const amount = value => value===null||value===undefined?'未提供':`${value} GB`;
 const duration = milliseconds => {
  if(!Number.isFinite(milliseconds)||milliseconds<0)return '未提供';
  const minutes=Math.floor(milliseconds/60000);return `${Math.floor(minutes/1440)}天${Math.floor(minutes%1440/60)}时${minutes%60}分`;
 };
 const timestamp = value => {
  if(!Number.isFinite(value)||value<=0)return '未提供';const date=new Date(value);
  return Number.isNaN(date.getTime())?'未提供':date.toLocaleString('zh-CN',{hour12:false});
 };
 let holdPolling=false,holdTimer;
 const syncHolds=async()=>{
  if(holdPolling)return;
  if(loading){clearTimeout(holdTimer);holdTimer=setTimeout(syncHolds,5000);return;}
  holdPolling=true;clearTimeout(holdTimer);
  const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),4000);
  const revision=holdRevision,generation=detailGeneration;
  try{
   const values=await request('/api/desktops/holds',undefined,controller.signal);
   if(loading||revision!==holdRevision||generation!==detailGeneration)return;
   const holds=new Map(values.map(item=>[`${item.account_id}:${item.desktop_id}`,item.hold]));
   for(const d of desktops){const hold=holds.get(key(d))||'';if((d.hold||'')!==hold){d.hold=hold;updateCard(d);}}
  }catch(_){}finally{clearTimeout(timeout);holdPolling=false;holdTimer=setTimeout(syncHolds,5000);}
 };
 document.addEventListener('visibilitychange',()=>{if(!document.hidden)void syncHolds();});
 window.addEventListener('focus',()=>void syncHolds());
 const openDesktop=async d=>{
  if(!confirm(`打开 ${d.name} 的官方网页桌面？将自动带入该账号的登录态。使用网页桌面期间暂停保活，关闭后自动恢复；本账号正在运行的登录/时长任务会中止。`))return;
  const popup=window.open('about:blank','_blank');if(!popup){notice('请允许弹出窗口后重试');return;}
  popup.opener=null;
  try{const result=await request('/api/desktops/open',{account_id:d.account_id,desktop_id:d.id});const url=new URL(result.url,window.location.origin);if(url.origin!==window.location.origin||!url.pathname.startsWith('/desktop-view/'))throw new Error('桌面地址无效');if(!result.entry_key)throw new Error('桌面入口验证信息缺失');const form=popup.document.createElement('form');form.method='POST';form.action=url.href;const input=popup.document.createElement('input');input.type='hidden';input.name='entry_key';input.value=result.entry_key;form.append(input);popup.document.body.replaceChildren(form);form.submit();d.hold='网页桌面使用中，关闭页面后自动恢复';render();notice('已打开官方网页桌面');holdRevision++;void syncHolds();}catch(e){popup.close();notice(e.message);}
 };
 const resume=async d=>{try{await request('/api/desktops/resume',{account_id:d.account_id,desktop_id:d.id});holdRevision++;d.hold='';render();notice('已解除暂停，将按账号保活配置恢复连接');}catch(e){notice(e.message);}};
 let jobTimer,jobsSnapshot;
 const loadJobs=async()=>{
  if(polling)return;polling=true;clearTimeout(jobTimer);let nextRefresh=5000;
  try{
   const jobs=await request('/api/desktops/jobs');const host=find('[data-cloud-jobs]');
   nextRefresh=jobs.some(j=>j.status==='running')?2000:5000;
   const snapshot=JSON.stringify(jobs);if(snapshot===jobsSnapshot)return;
   const previous=new Map([...host.querySelectorAll('details')].map(el=>[el.dataset.jobId,{open:el.open,scroll:el.querySelector('.cloud-job-items')?.scrollTop||0}]));
   host.replaceChildren();jobsSnapshot=snapshot;
   for(const j of jobs){
    const details=document.createElement('details');details.dataset.jobId=String(j.id);const old=previous.get(String(j.id));details.open=old?old.open:j.status==='running';
    const summary=document.createElement('summary');const count=s=>j.items.filter(i=>i.status===s).length;
    summary.textContent=`#${j.id} ${labels[j.action]||j.action} · ${j.items.length} 台 · 接受 ${count('accepted')} / 失败 ${count('failed')} / 待核对 ${count('unknown')} · ${j.status==='running'?'执行中':j.status==='finished'?'队列已结束':'已中断'}`;
    details.append(summary);const list=document.createElement('ul');list.className='cloud-job-items';
    for(const item of j.items){const li=document.createElement('li');li.textContent=`${item.account_name||`已删除账号（#${item.account_id}）`} / ${item.name||item.desktop_id}：${statuses[item.status]||item.status} · ${item.message}`;list.append(li);}
    details.append(list);host.append(details);if(old)list.scrollTop=old.scroll;
   }
  }catch(e){jobsSnapshot=undefined;find('[data-cloud-jobs]').textContent=e.message;nextRefresh=10000;}finally{polling=false;jobTimer=setTimeout(loadJobs,nextRefresh);}
 };
 find('[data-cloud-power]').addEventListener('click',async()=>{
  if(loading){notice('请等待云机列表查询完成');return;}
  if(!selected.size){notice('请先选择云机');return;}
  const action=find('[data-cloud-action]').value;
  const text=action==='shutdown'?'关机后自动连接会暂停，开机或恢复连接可解除。':action==='reboot'?'重启后自动连接暂停 5 分钟。':'';
  if(!confirm(`确认对选中的 ${selected.size} 台云机执行${labels[action]}？${text}同账号登录/时长任务会中止，关机或重启可能丢失未保存的数据。`))return;
  const b=find('[data-cloud-power]');b.disabled=true;
  try{const result=await request('/api/desktops/power',{action,targets:[...selected.values()]});notice(`任务 #${result.job_id} 已加入队列，请查看下方逐台结果`);holdRevision++;selected.clear();render();void loadJobs();setTimeout(loadJobs,500);}catch(e){notice(e.message);}finally{b.disabled=false;}
 });
 find('[data-cloud-refresh]').addEventListener('click',refresh);
 find('[data-cloud-account]').addEventListener('change',()=>{selected.clear();page=1;render();});find('[data-cloud-search]').addEventListener('input',()=>{selected.clear();page=1;render();});
 find('[data-cloud-select-all]').addEventListener('change',e=>{for(const d of filtered().filter(d=>!d.forbidden)){if(e.target.checked)selected.set(key(d),{account_id:d.account_id,desktop_id:d.id});else selected.delete(key(d));}render();});
 find('[data-cloud-prev]').addEventListener('click',()=>{page--;render();});find('[data-cloud-next]').addEventListener('click',()=>{page++;render();});
 void refresh();void loadJobs();holdTimer=setTimeout(syncHolds,5000);
})();
