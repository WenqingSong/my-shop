// Framework motion v13: Gaussian dissolution on readiness; v12 geometry preserved.
const FRAMEWORK_MOTION={"duration":5400,"stroke":10.5,"timing":{"formStart":0.035,"logoStart":0.4,"exitStart":0.6,"exitEnd":0.92},"points":{"A":[128,28],"B":[44,76.4974],"C":[212,76.4974],"D":[128,124.9948],"E":[44,173.4923],"L":[112.5622,213.077],"S":[128,229],"R":[212,157]},"clips":{"left":"M0 0H256V180H106.5V256H0Z","spine":"M0 0H256V295.8908L0 148.0892Z","right":"M0 0H256V123.84L0 271.64Z"},"edges":[{"id":"AB","from":"A","to":"B","exit":[0,0.18]},{"id":"AC","from":"A","to":"C","exit":[0,0.18]},{"id":"BE","from":"B","to":"E","exit":[0.18,0.44]},{"id":"BD","from":"B","to":"D","exit":[0.18,0.4]},{"id":"DF","from":"D","to":"S","clip":"spine","visibleEnd":0.9325986565785171,"exit":[0.4,0.77]},{"id":"EF","from":"E","to":"L","clip":"left","visibleEnd":0.911580433533638,"exit":[0.44,0.66]},{"id":"CG","from":"C","to":"R","clip":"right","visibleEnd":0.9036444375712088,"exit":[0.18,1]}],"routes":{"left":{"knots":[[0.035,0],[0.13,1],[0.24,2],[0.38,3]],"tangents":{"0":0,"1":8,"3":0}},"spine":{"knots":[[0.035,0],[0.13,1],[0.24,2],[0.38,3]],"tangents":{"0":0,"1":8,"3":0}},"right":{"knots":[[0.035,0],[0.13,1],[0.36,2]],"tangents":{"0":0,"1":8,"2":0}}}};
// Visual phases and playback lifecycle are separate. Normal playback always
// reaches the approved open logo, then holds it before any withdrawal starts.
const motionClamp=v=>Math.max(0,Math.min(1,v));
const motionEase=v=>{v=motionClamp(v);return v*v*(3-2*v);};
const motionFlow=v=>{v=motionClamp(v);return v*v*(2-v);};
function motionRoute(route){
 const k=route.knots,n=k.length,h=[],delta=[],slopes=new Array(n).fill(0);
 for(let i=0;i<n-1;i++){h[i]=k[i+1][0]-k[i][0];delta[i]=(k[i+1][1]-k[i][1])/h[i];}
 for(let i=1;i<n-1;i++)if(delta[i-1]*delta[i]>0){const a=2*h[i]+h[i-1],b=h[i]+2*h[i-1];slopes[i]=(a+b)/(a/delta[i-1]+b/delta[i]);}
 for(const [index,value] of Object.entries(route.tangents))slopes[Number(index)]=value;
 return p=>{
  if(p<=k[0][0])return k[0][1];if(p>=k[n-1][0])return k[n-1][1];
  let i=0;while(p>k[i+1][0])i++;
  const t=(p-k[i][0])/h[i],t2=t*t,t3=t2*t;
  return (2*t3-3*t2+1)*k[i][1]+(t3-2*t2+t)*h[i]*slopes[i]+(-2*t3+3*t2)*k[i+1][1]+(t3-t2)*h[i]*slopes[i+1];
 };
}
const motionRoutes=Object.fromEntries(Object.entries(FRAMEWORK_MOTION.routes).map(([name,route])=>[name,motionRoute(route)]));
function motionHeads(p){
 const left=motionRoutes.left(p),spine=motionRoutes.spine(p),right=motionRoutes.right(p);
 return {AB:motionClamp(left),AC:motionClamp(right),BE:motionClamp(left-1),BD:motionClamp(spine-1),DF:motionClamp(spine-2),EF:motionClamp(left-2),CG:motionClamp(right-1)};
}
function motionPhase(p){
 const t=FRAMEWORK_MOTION.timing;
 return p<t.formStart?'blank':p<t.logoStart?'forming':p<t.exitStart?'logo':p<t.exitEnd?'exiting':'blank';
}
function motionGeometry(heads,exitProgress=null){
 const m=FRAMEWORK_MOTION;
 return m.edges.map(e=>{
  const a=m.points[e.from],b=m.points[e.to],head=heads[e.id];
  const visibleHead=Math.min(head,e.visibleEnd??1);
  const tail=exitProgress===null?0:Math.min(visibleHead,motionFlow((exitProgress-e.exit[0])/(e.exit[1]-e.exit[0])));
  const length=Math.max(0,visibleHead-tail)*Math.hypot(b[0]-a[0],b[1]-a[1]);
  const start=[a[0]+(b[0]-a[0])*tail,a[1]+(b[1]-a[1])*tail];
  const end=[a[0]+(b[0]-a[0])*head,a[1]+(b[1]-a[1])*head];
  return {id:e.id,length,d:`M${start[0].toFixed(5)} ${start[1].toFixed(5)}L${end[0].toFixed(5)} ${end[1].toFixed(5)}`,width:m.stroke*motionEase(length/(exitProgress===null?14:28))};
 });
}
function motionLogoState(){return motionGeometry(Object.fromEntries(FRAMEWORK_MOTION.edges.map(e=>[e.id,1])));}
function motionVisual(p){
 const t=FRAMEWORK_MOTION.timing,exit=p>=t.exitStart?motionClamp((p-t.exitStart)/(t.exitEnd-t.exitStart)):null;
 const focus=motionEase((p-.06)/(t.logoStart-.06));
 return {p,phase:motionPhase(p),heads:motionHeads(p),exit,
  geometry:motionGeometry(motionHeads(p),exit),
  opacity:motionEase((p-t.formStart)/.15),blur:exit===null?3.4*(1-focus):.35*motionEase(exit),
  glow:.30*(1-focus),glowBlur:2.5+7*(1-focus)};
}
function motionInverseFlow(value){let low=0,high=1;for(let i=0;i<40;i++){const mid=(low+high)/2;if(motionFlow(mid)<value)low=mid;else high=mid;}return (low+high)/2;}
let frameworkMotionId=0;
class FrameworkMotionPlayer{
 constructor(svg,{speed=1,loop=false}={}){
  this.svg=svg;this.nodes=new Map([...svg.querySelectorAll('[data-edge]')].map(p=>[p.dataset.edge,p]));
  this.core=svg.querySelector('[data-core]');this.halo=svg.querySelector('[data-halo]');
  this.coreBlur=svg.querySelector('[data-core-blur]');this.haloBlur=svg.querySelector('[data-halo-blur]');
  this._speed=1;this.loop=Boolean(loop);this._time=0;this._started=performance.now();this._paused=false;
  this._surface=null;this._finished=false;this._cancelled=false;this._finish=null;this._finishPromise=null;this._resolveFinish=null;this._timer=null;this.frame=null;
  this._reduced=globalThis.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches??false;
  this.speed=speed;
  this.step=()=>{
   this.frame=null;if(this._cancelled||this._finished)return;
   if(this._finish){const q=motionClamp((performance.now()-this._finish.started)/this._finish.duration);this._drawFinish(q);if(q>=1){this._complete();return;}}
   else if(!this._paused){const time=this.currentTime;if(!this.loop&&time>=FRAMEWORK_MOTION.duration){this._complete();return;}this.draw(time);}
   if(this._finish||!this._paused)this._queue();
  };
  this._visibility=()=>{if(document.hidden&&this._finish)this._complete();};this._listening=false;this._listen();
  if(this._reduced){this._paused=true;this._time=FRAMEWORK_MOTION.timing.logoStart*FRAMEWORK_MOTION.duration;this.draw(this._time);}
  else{this.draw(0);this._queue();}
 }
 get state(){return this._cancelled?'cancelled':this._finished?'finished':this._finish?'finishing':this._paused?'paused':'playing';}
 get phase(){return this._cancelled?'cancelled':this._finished?'ended':this._finish?'exiting':this._rendered.phase;}
 get speed(){return this._speed;}
 set speed(value){
  if(typeof value!=='number'||!Number.isFinite(value)||value<=0)throw new RangeError('speed must be a positive finite number');
  this._time=this.currentTime;this._started=performance.now();this._speed=value;
 }
 get currentTime(){const time=this._paused||this._finish||this._finished||this._cancelled?this._time:this._time+(performance.now()-this._started)*this._speed;return this.loop?time:Math.min(time,FRAMEWORK_MOTION.duration);}
 set currentTime(value){
  if(!Number.isFinite(value)||value<0)throw new RangeError('currentTime must be finite and non-negative');
  if(this._finish||this._cancelled)return;if(this._finished)throw new Error('Call restart() before seeking a finished animation');
  this._time=this.loop?value:Math.min(value,FRAMEWORK_MOTION.duration);this._started=performance.now();this.draw(this._time);
 }
 _listen(){if(!this._listening){document.addEventListener('visibilitychange',this._visibility);this._listening=true;}}
 _unlisten(){if(this._listening){document.removeEventListener('visibilitychange',this._visibility);this._listening=false;}}
 _queue(){if(this.frame===null)this.frame=requestAnimationFrame(this.step);}
 _stopFrame(){if(this.frame!==null)cancelAnimationFrame(this.frame);this.frame=null;}
 _status(){this.svg.setAttribute('data-motion-state',this.state);this.svg.setAttribute('data-motion-phase',this.phase);}
 _apply(visual){
  this._rendered=visual;
  for(const s of visual.geometry){const node=this.nodes.get(s.id);node.setAttribute('d',s.d);node.setAttribute('stroke-width',s.width.toFixed(5));}
  this.core.setAttribute('opacity',visual.opacity.toFixed(5));this.halo.setAttribute('opacity',(visual.glow*visual.opacity).toFixed(5));
  this.coreBlur.setAttribute('stdDeviation',visual.blur.toFixed(5));this.haloBlur.setAttribute('stdDeviation',visual.glowBlur.toFixed(5));this._status();
 }
 draw(ms){const d=FRAMEWORK_MOTION.duration,remainder=ms%d;const p=this.loop?(ms>0&&remainder===0?1:remainder/d):motionClamp(ms/d);this._apply(motionVisual(p));}
 pause(){if(this._paused||this._finish||this._finished||this._cancelled)return;this._time=this.currentTime;this._paused=true;this._stopFrame();this.draw(this._time);}
 play(){if(!this._paused||this._finish||this._finished||this._cancelled||this._reduced)return;this._started=performance.now();this._paused=false;this._status();this._queue();}
 _restoreSurface(){
  if(!this._surface)return;
  const {element,original}=this._surface;
  Object.assign(element.style,original);this._surface=null;
 }
 restart(){
  if(this._cancelled)throw new Error('A cancelled animation cannot be restarted');
  this._restoreSurface();
  this._stopFrame();clearTimeout(this._timer);this._timer=null;this._resolveFinish?.();this._resolveFinish=null;
  this._finish=null;this._finishPromise=null;this._finished=false;this._started=performance.now();this._paused=this._reduced;
  this._time=this._reduced?FRAMEWORK_MOTION.timing.logoStart*FRAMEWORK_MOTION.duration:0;this._listen();this.draw(this._time);if(!this._reduced)this._queue();
 }
 finish({surface}={}){
  if(this._finishPromise)return this._finishPromise;
  if(surface&&!surface.style)throw new TypeError('finish surface must be a styled element');
  this._finishPromise=new Promise(resolve=>{this._resolveFinish=resolve;});
  if(this._cancelled||this._finished){this._resolveFinish();this._resolveFinish=null;return this._finishPromise;}
  this._time=this.currentTime;this._paused=false;this._stopFrame();
  const snapshot=this._rendered;
  if(surface){
   this._surface={element:surface,original:{opacity:surface.style.opacity,filter:surface.style.filter,willChange:surface.style.willChange,transition:surface.style.transition}};
   surface.style.transition='none';surface.style.willChange='filter, opacity';
  }
  // A full loading surface includes its lettering and background, so dissolve
  // it even when the logo happens to be in a natural blank phase.
  const visible=snapshot.opacity>0&&snapshot.geometry.some(e=>e.width>0&&e.length>0);
  if(this._reduced||document.hidden||(!surface&&!visible)){this._complete();return this._finishPromise;}
  this._finish={snapshot,started:performance.now(),duration:640};
  this._drawFinish(0);this._timer=setTimeout(()=>this._complete(),640);this._queue();return this._finishPromise;
 }
 _drawFinish(progress){
  const snapshot=this._finish.snapshot,q=motionEase(progress);
  // Freeze every path and its stroke width: the mark dissolves without
  // retracting, scaling, completing unseen edges, or blurring the next scene.
  this._apply({...snapshot,phase:'exiting',opacity:this._surface?snapshot.opacity:snapshot.opacity*(1-q),blur:this._surface?snapshot.blur:snapshot.blur+12*q,glow:snapshot.glow*(1-q)});
  if(this._surface){
   this._surface.element.style.opacity=(1-q).toFixed(5);
   this._surface.element.style.filter=`blur(${(14*q).toFixed(3)}px)`;
  }
 }
 _complete(){
  if(this._finished||this._cancelled)return;
  clearTimeout(this._timer);this._timer=null;this._stopFrame();this._unlisten();
  this._finished=true;this._paused=false;this._finish=null;this._time=FRAMEWORK_MOTION.duration;this._apply(motionVisual(1));
  if(this._surface){const {element,original}=this._surface;element.style.opacity='0';element.style.filter='blur(14px)';element.style.willChange=original.willChange;}
  this._resolveFinish?.();this._resolveFinish=null;
 }
 cancel(){
  if(this._cancelled)return;this._restoreSurface();this._time=this.currentTime;this._cancelled=true;this._finish=null;
  this._stopFrame();clearTimeout(this._timer);this._timer=null;this._unlisten();this._status();this._resolveFinish?.();this._resolveFinish=null;
 }
}
globalThis.FrameworkMotion=Object.freeze({
 version:13,duration:FRAMEWORK_MOTION.duration,exitDuration:640,
 create(svg,options={}){
  if(svg?.tagName?.toLowerCase()!=='svg')throw new TypeError('FrameworkMotion.create expects an SVG element');
  const ns='http://www.w3.org/2000/svg',prefix=`framework-${++frameworkMotionId}`;
  const el=(tag,attrs,parent)=>{const n=document.createElementNS(ns,tag);for(const [k,v] of Object.entries(attrs))n.setAttribute(k,v);parent.appendChild(n);return n;};
  // An instance owns its nodes; cancel an old instance before reusing its SVG.
  svg.replaceChildren();const defs=el('defs',{},svg);
  for(const [name,d] of Object.entries(FRAMEWORK_MOTION.clips))el('path',{d},el('clipPath',{id:`${prefix}-${name}`},defs));
  for(const name of ['core','halo'])el('feGaussianBlur',{[`data-${name}-blur`]:'',stdDeviation:0},el('filter',{id:`${prefix}-${name}-filter`,x:'-50%',y:'-50%',width:'200%',height:'200%','color-interpolation-filters':'sRGB'},defs));
  const geometry=el('g',{id:`${prefix}-geometry`,transform:'translate(6 0)',fill:'none',stroke:'currentColor','stroke-linecap':'round','stroke-linejoin':'round'},defs);
  for(const edge of FRAMEWORK_MOTION.edges)el('path',{'data-edge':edge.id,'stroke-width':0,...(edge.clip?{'clip-path':`url(#${prefix}-${edge.clip})`}:{})},geometry);
  el('use',{href:`#${prefix}-geometry`,'data-halo':'',filter:`url(#${prefix}-halo-filter)`,opacity:0},svg);
  el('use',{href:`#${prefix}-geometry`,'data-core':'',filter:`url(#${prefix}-core-filter)`},svg);
  svg.setAttribute('viewBox','0 0 256 256');return new FrameworkMotionPlayer(svg,options);
 }
});
