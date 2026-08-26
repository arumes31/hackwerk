(()=>{
const NS="http://www.w3.org/2000/svg", sleep=ms=>new Promise(r=>setTimeout(r,ms)), reduce=matchMedia("(prefers-reduced-motion: reduce)").matches;
const roadLayer=document.getElementById("roads"), forestLayer=document.getElementById("forestDynamic"), logsLayer=document.getElementById("logs"), pileLayer=document.getElementById("piles"), vehicleLayer=document.getElementById("vehicles");

function E(tag,a={}){const e=document.createElementNS(NS,tag);for(const[k,v]of Object.entries(a))e.setAttribute(k,v);return e}
function rev(a){return[...a].reverse()}
function dist(a,b){return Math.hypot(b[0]-a[0],b[1]-a[1])}
function plen(p){let n=0;for(let i=1;i<p.length;i++)n+=dist(p[i-1],p[i]);return n}

function catmull(points,samples=12){
 if(points.length<3)return densify(points,14);
 const ext=[points[0],...points,points.at(-1)],out=[];
 for(let i=0;i<ext.length-3;i++){
   const p0=ext[i],p1=ext[i+1],p2=ext[i+2],p3=ext[i+3];
   for(let j=0;j<samples;j++){
     const t=j/samples,t2=t*t,t3=t2*t;
     const x=.5*((2*p1[0])+(-p0[0]+p2[0])*t+(2*p0[0]-5*p1[0]+4*p2[0]-p3[0])*t2+(-p0[0]+3*p1[0]-3*p2[0]+p3[0])*t3);
     const y=.5*((2*p1[1])+(-p0[1]+p2[1])*t+(2*p0[1]-5*p1[1]+4*p2[1]-p3[1])*t2+(-p0[1]+3*p1[1]-3*p2[1]+p3[1])*t3);
     const l=out.at(-1);if(!l||dist(l,[x,y])>1.5)out.push([x,y]);
   }
 }
 out.push(points.at(-1));return densify(out,13)
}

function densify(points,max=15){
 const out=[points[0]];
 for(let i=1;i<points.length;i++){
   const a=points[i-1],b=points[i],d=dist(a,b),parts=Math.max(1,Math.ceil(d/max));
   for(let j=1;j<=parts;j++){const t=j/parts;out.push([a[0]+(b[0]-a[0])*t,a[1]+(b[1]-a[1])*t])}
 }
 return out
}

/* Graph Nodes & Road Topology */
const P={
 HQ:[595,425],TD:[690,425],MW:[310,455],ME:[900,430],MS:[610,630],
 C:[600,395],W:[405,365],WW:[205,395],E:[790,385],EE:[985,350],N:[605,210],NW:[405,205],NE:[805,205],S:[600,585],SW:[385,610],SE:[815,610]
};
const edges=[];
function edge(a,b,controls,type="road"){const pts=catmull(controls,12);edges.push({a,b,pts,type,cost:plen(pts)})}

edge("HQ","C",[P.HQ,[595,410],P.C]);
edge("TD","C",[P.TD,[650,410],P.C]);
edge("MW","WW",[P.MW,[285,442],[250,418],P.WW]);
edge("ME","EE",[P.ME,[925,408],[952,380],P.EE]);
edge("MS","S",[P.MS,[610,612],[606,600],P.S]);
edge("WW","W",[P.WW,[275,382],[340,370],P.W]);
edge("W","C",[P.W,[470,370],[535,385],P.C]);
edge("C","E",[P.C,[665,405],[730,398],P.E]);
edge("E","EE",[P.E,[855,378],[920,360],P.EE]);
edge("C","N",[P.C,[590,330],[598,270],P.N]);
edge("N","NW",[P.N,[540,200],[470,198],P.NW]);
edge("N","NE",[P.N,[675,200],[740,198],P.NE]);
edge("C","S",[P.C,[610,465],[605,525],P.S]);
edge("S","SW",[P.S,[530,600],[455,608],P.SW]);
edge("S","SE",[P.S,[670,600],[745,607],P.SE]);

/* Forests: gate = Waldrand, work = Fällpunkt IM Wald, load = Ladepunkt IM Wald */
const forests=[
 {base:"NW",gate:[150,160],work:[145,132],pilePt:[118,120],load:[134,148],c:[131,126]},
 {base:"NW",gate:[330,165],work:[330,130],pilePt:[300,118],load:[315,148],c:[315,124]},
 {base:"N", gate:[510,165],work:[530,126],pilePt:[500,112],load:[517,145],c:[515,119]},
 {base:"N", gate:[700,165],work:[738,130],pilePt:[710,112],load:[720,148],c:[724,121]},
 {base:"NE",gate:[890,165],work:[935,126],pilePt:[905,112],load:[918,145],c:[920,119]},
 {base:"NE",gate:[1070,175],work:[1118,142],pilePt:[1090,125],load:[1100,158],c:[1104,133]},
 {base:"WW",gate:[155,610],work:[128,696],pilePt:[100,682],load:[118,662],c:[114,689]},
 {base:"SW",gate:[315,640],work:[330,706],pilePt:[300,692],load:[316,671],c:[315,699]},
 {base:"SW",gate:[505,640],work:[530,708],pilePt:[500,696],load:[516,674],c:[515,702]},
 {base:"SE",gate:[735,640],work:[760,708],pilePt:[730,696],load:[747,673],c:[745,702]},
 {base:"SE",gate:[925,635],work:[950,706],pilePt:[920,692],load:[938,671],c:[935,699]},
 {base:"EE",gate:[1070,610],work:[1128,698],pilePt:[1100,685],load:[1115,662],c:[1114,691]}
];
forests.forEach((f,i)=>{f.id=i;P["G"+i]=f.gate;P["T"+i]=f.work;P["U"+i]=f.load});

const approachControls=[
[[405,205],[305,190],[220,175],[150,160]],[[405,205],[365,190],[345,175],[330,165]],[[605,210],[570,190],[535,178],[510,165]],
[[605,210],[640,188],[675,175],[700,165]],[[805,205],[835,188],[865,177],[890,165]],[[805,205],[900,195],[990,188],[1070,175]],
[[205,395],[185,470],[170,540],[155,610]],[[385,610],[360,620],[335,630],[315,640]],[[385,610],[425,620],[465,630],[505,640]],
[[815,610],[785,620],[760,630],[735,640]],[[815,610],[850,620],[890,628],[925,635]],[[985,350],[1020,430],[1045,520],[1070,610]]
];
forests.forEach((f,i)=>{
 edge(f.base,"G"+i,approachControls[i],"track");
 edge("G"+i,"T"+i,[f.gate,[(f.gate[0]+f.work[0])/2,(f.gate[1]+f.work[1])/2],f.work],"track");
 edge("G"+i,"U"+i,[f.gate,[(f.gate[0]+f.load[0])/2,(f.gate[1]+f.load[1])/2],f.load],"track");
});

/* Draw Roads with Center Line Markings */
for(const e of edges){
 const pts=e.pts.map(x=>x.join(",")).join(",");
 roadLayer.appendChild(E("polyline",{points:pts,class:e.type==="road"?"road-shoulder":"track-outline"}));
 roadLayer.appendChild(E("polyline",{points:pts,class:e.type==="road"?"road-outline":"track-outline"}));
}
for(const [name,p] of Object.entries(P)){
 const incident=edges.filter(e=>e.a===name||e.b===name);if(!incident.length)continue;
 const road=incident.some(e=>e.type==="road"),r=road?11.5:6.7;
 roadLayer.appendChild(E("circle",{cx:p[0],cy:p[1],r,class:road?"junction-outline":"track-junction-outline"}));
}
for(const e of edges.filter(e=>e.type==="track"))roadLayer.appendChild(E("polyline",{points:e.pts.map(x=>x.join(",")).join(","),class:"track-fill"}));
for(const e of edges.filter(e=>e.type==="road")){
 roadLayer.appendChild(E("polyline",{points:e.pts.map(x=>x.join(",")).join(","),class:"road-fill"}));
 roadLayer.appendChild(E("polyline",{points:e.pts.map(x=>x.join(",")).join(","),class:"road-line"}));
}
for(const [name,p] of Object.entries(P)){
 const incident=edges.filter(e=>e.a===name||e.b===name);if(!incident.length)continue;
 const road=incident.some(e=>e.type==="road"),r=road?6.6:3.9;
 roadLayer.appendChild(E("circle",{cx:p[0],cy:p[1],r,class:road?"junction-fill":"track-junction-fill"}));
}

/* Architectural Vector Generators */
const buildingLayer=document.getElementById("buildings");
function buildingGroup(node,dx,dy,type){
 const base=P[node];
 const g=E("g",{transform:`translate(${base[0]+dx} ${base[1]+dy})`});
 if(type==="hq"){
   g.innerHTML=`
     <ellipse cx="0" cy="22" rx="48" ry="12" fill="rgba(0,0,0,0.22)"/>
     <rect class="building" x="-36" y="-18" width="72" height="40" rx="4" fill="#2d3a31"/>
     <path class="roof-hq" d="M-42 -18 L0 -48 L42 -18 Z"/>
     <path d="M-42 -18 L0 -48 L0 -18 Z" fill="rgba(255,255,255,0.18)"/>
     <polygon points="-28,-26 -8,-40 -8,-26" fill="url(#solarPattern)" opacity="0.9"/>
     <polygon points="8,-26 28,-26 8,-40" fill="url(#solarPattern)" opacity="0.9"/>
     <rect class="glass-panel" x="-26" y="-8" width="14" height="12" rx="1"/>
     <rect class="glass-panel" x="12" y="-8" width="14" height="12" rx="1"/>
     <rect x="-8" y="2" width="16" height="20" fill="#1e4a36" rx="1"/>
     <rect x="-12" y="2" width="24" height="3" fill="#d8952f"/>
     <rect x="-28" y="-38" width="6" height="14" fill="#444"/>
     <circle class="smoke-puff" cx="-25" cy="-42" r="5" fill="#ddd"/>
     <circle class="smoke-puff" cx="-25" cy="-42" r="7" fill="#eee" style="animation-delay: 0.9s;"/>
   `;
 }else if(type==="depot"){
   g.innerHTML=`
     <ellipse cx="0" cy="18" rx="38" ry="10" fill="rgba(0,0,0,0.22)"/>
     <rect class="building" x="-28" y="-14" width="56" height="32" rx="3" fill="#47524c"/>
     <path class="roof-depot" d="M-34 -14 L0 -34 L34 -14 Z"/>
     <path d="M-34 -14 L0 -34 L0 -14 Z" fill="rgba(255,255,255,0.15)"/>
     <rect x="-20" y="-3" width="12" height="21" fill="#313834" rx="1"/>
     <line x1="-20" y1="4" x2="-8" y2="4" stroke="#555" stroke-width="1"/>
     <line x1="-20" y1="10" x2="-8" y2="10" stroke="#555" stroke-width="1"/>
     <rect x="8" y="-3" width="12" height="21" fill="#313834" rx="1"/>
     <line x1="8" y1="4" x2="20" y2="4" stroke="#555" stroke-width="1"/>
     <line x1="8" y1="10" x2="20" y2="10" stroke="#555" stroke-width="1"/>
     <rect x="-24" y="-3" width="48" height="3" fill="#d8952f"/>
   `;
 }else{
   g.innerHTML=`
     <ellipse cx="0" cy="16" rx="32" ry="9" fill="rgba(0,0,0,0.22)"/>
     <rect class="building" x="-25" y="-12" width="50" height="28" rx="3" fill="#526338"/>
     <path class="roof-machine" d="M-30 -12 L0 -32 L30 -12 Z"/>
     <path d="M-30 -12 L0 -32 L0 -12 Z" fill="rgba(255,255,255,0.15)"/>
     <rect x="-8" y="0" width="16" height="16" fill="#d8952f" rx="1"/>
     <rect x="-6" y="2" width="12" height="14" fill="#2b331f"/>
   `;
 }
 buildingLayer.appendChild(g);
 return g;
}
buildingGroup("HQ",-44,20,"hq");
buildingGroup("TD",38,22,"depot");
buildingGroup("MW",-30,24,"machine");
buildingGroup("ME",34,24,"machine");
buildingGroup("MS",36,20,"machine");

function shortest(start,end){
 if(start===end)return[P[start]];
 const d={},prev={},q=new Set(Object.keys(P));for(const n of q)d[n]=Infinity;d[start]=0;
 while(q.size){
   let u=null,b=Infinity;for(const n of q)if(d[n]<b){b=d[n];u=n}if(u===null)break;q.delete(u);if(u===end)break;
   for(const e of edges){
     let v=null,pts=null;if(e.a===u){v=e.b;pts=e.pts}else if(e.b===u){v=e.a;pts=rev(e.pts)}else continue;
     if(!q.has(v))continue;const alt=d[u]+e.cost;
     if(alt<d[v]){d[v]=alt;prev[v]={u,pts}}
   }
 }
 const segs=[];let cur=end;while(cur!==start){const x=prev[cur];if(!x)return[];segs.push(x.pts);cur=x.u}
 segs.reverse();const out=[];for(const s of segs)for(const p of s){const l=out.at(-1);if(!l||dist(l,p)>.2)out.push(p)}return out
}

/* Tree Visual Generators */
const TREE_ARTS=[
 s=>`<ellipse cx="0" cy="5" rx="11" ry="4.5" fill="rgba(0,0,0,0.16)"/><rect x="-2.2" y="-2" width="4.4" height="12" fill="#5d3a20" rx="1.6"/><path d="M-14,-2 C-14,-11 -8,-15 0,-27 C8,-15 14,-11 14,-2 Z" fill="url(#pineGradDark)"/><path d="M-10,-7 C-10,-14 -6,-18 0,-27 C6,-18 10,-14 10,-7 Z" fill="url(#pineGradLight)"/><path d="M-6,-12 C-6,-18 -3,-21 0,-27 C3,-21 6,-18 6,-12 Z" fill="#5aa878"/><path d="M0,-27 C4,-19 7,-16 7,-11 L4,-11 C4,-15 2,-19 0,-23 Z" fill="rgba(255,255,255,0.22)"/>`,
 s=>`<ellipse cx="0" cy="4.5" rx="10" ry="4" fill="rgba(0,0,0,0.16)"/><rect x="-2.2" y="-2" width="4.4" height="12" fill="#5d3a20" rx="1.6"/><path d="M-12,-2 L0,-25 L12,-2 Z" fill="url(#pineGradDark)"/><path d="M-9,-9 L0,-27 L9,-9 Z" fill="url(#pineGradLight)"/><path d="M0,-27 L9,-9 L5,-9 L0,-21 Z" fill="rgba(255,255,255,0.2)"/>`,
 s=>`<ellipse cx="0" cy="4" rx="9" ry="3.5" fill="rgba(0,0,0,0.16)"/><rect x="-1.8" y="-3" width="3.6" height="13" fill="#5d3a20" rx="1.4"/><circle cx="-5" cy="-13" r="7.5" fill="url(#pineGradLight)"/><circle cx="5" cy="-14" r="7.5" fill="url(#leafGrad)"/><circle cy="-21" r="8" fill="url(#leafGrad)"/><circle cx="2.5" cy="-23.5" r="3" fill="rgba(255,255,255,0.25)"/>`
];

function tree(x,y,i){
 const pos=E("g",{transform:`translate(${x} ${y})`,class:"tree-pos"});
 const art=E("g",{class:"tree-art"});
 const scale=.82+((i*7)%5)*.11;
 art.innerHTML=`<g transform="scale(${scale.toFixed(2)})">${TREE_ARTS[i%3]()}</g>`;
 pos.appendChild(art);
 pos._art=art;
 forestLayer.appendChild(pos);
 return{pos,art,x,y};
}

function stump(x,y){
 const g=E("g",{transform:`translate(${x} ${y})`,class:"stump"});
 g.innerHTML='<ellipse rx="5.5" ry="2.8" fill="#c08e61"/><rect x="-4.5" y="0" width="9" height="5.5" fill="#6e4428"/>';
 forestLayer.appendChild(g);
 return g
}

function felledLog(x,y,i){
 const pos=E("g",{transform:`translate(${x} ${y}) rotate(${(i%2?1:-1)*(8+(i%5)*4)})`,class:"felled-log-pos"});
 const len=23+(i%4)*3, bark=i%2?"#785237":"#6e4428";
 pos.innerHTML=`
   <rect x="${-len/2}" y="-3.8" width="${len}" height="7.6" rx="3.8" fill="${bark}"/>
   <ellipse cx="${-len/2}" cy="0" rx="3.8" ry="3.8" fill="#c99467"/>
   <circle cx="${-len/2}" cy="0" r="2" fill="none" stroke="#a36e43" stroke-width="0.8"/>
   <ellipse cx="${len/2}" cy="0" rx="3.8" ry="3.8" fill="#bf885c"/>
   <path d="M${-len/2+5} -1 H${len/2-5}" stroke="#916442" stroke-width="1.2" opacity=".7"/>
 `;
 logsLayer.appendChild(pos);
 return pos;
}

/* Hackschnitzelhaufen: wächst stufenweise mit jedem gefällten Baum */
function chipPile(x,y){
 const pos=E("g",{transform:`translate(${x} ${y})`,class:"chip-pile-pos"});
 const art=E("g",{class:"chip-pile-art"});
 art.innerHTML=`
   <ellipse cx="2" cy="9" rx="26" ry="5" fill="rgba(0,0,0,0.15)"/>
   <path d="M-24 8 C-20 -6 -8 -12 0 -11 C5 -19 17 -17 21 -2 C26 -1 28 4 29 8 Z" fill="#d8952f"/>
   <path d="M-24 8 C-20 -6 -8 -12 0 -11 L0 8 Z" fill="#c8862a"/>
   <path d="M-14 3h7m5-8h7m6 9h7" stroke="#9b4931" stroke-width="2" stroke-linecap="round" opacity=".8"/>
   <circle cx="-8" cy="-3" r="1.2" fill="#a86a1d"/><circle cx="6" cy="-7" r="1.2" fill="#a86a1d"/><circle cx="13" cy="2" r="1.2" fill="#a86a1d"/>
 `;
 pos.appendChild(art);
 pos._art=art;
 logsLayer.appendChild(pos);
 return pos;
}
function chipPileGrow(pos,level){
 pos._art.style.transform=`scale(${(.25+.75*level).toFixed(3)})`;
 pos.classList.add("show");
}

function forestPositions(c,count=30){
 const out=[];
 for(let i=0;i<count;i++){
   const a=i*2.399963,r=Math.sqrt((i+1)/count);
   out.push([c[0]+Math.cos(a)*r*46,c[1]+Math.sin(a)*r*32])
 }
 return out
}

function visuals(f){
 const poly=document.getElementById("forest"+f.id);
 const pts=forestPositions(f.c);
 const trees=pts.map((p,i)=>{
   const t=tree(p[0],p[1],i);
   t.stump=stump(p[0],p[1]);
   t.log=felledLog(p[0],p[1]+5,i);
   return t;
 });
 const chips=chipPile(f.pilePt[0]+20,f.pilePt[1]+14);
 return{poly,trees,chips,felled:[],state:"grown"};
}

/* Vehicle Graphic Generators */
function wheel(x,y,r,hub){
 return `<g class="wheel" transform="translate(${x} ${y})"><g class="wheel-spin"><circle r="${r}" fill="#18201a"/><line x1="${-r*.62}" y1="0" x2="${r*.62}" y2="0" stroke="#9aa29b" stroke-width="1.4"/><line x1="0" y1="${-r*.62}" x2="0" y2="${r*.62}" stroke="#9aa29b" stroke-width="1.4"/><circle r="${hub}" fill="#ccc"/></g></g>`;
}

function vehicle(kind,node){
 const g=E("g",{class:"vehicle"});
 if(kind==="tractor"){
   g.innerHTML=`
     <polygon class="headlight" points="35,-5 95,-30 95,22"/>
     <g class="rig">
       <ellipse cx="10" cy="15" rx="30" ry="7.5" fill="rgba(0,0,0,0.2)"/>
       <rect x="-17" y="-10" width="27" height="17" fill="url(#cabGrad)" rx="3"/>
       <rect x="-6" y="-25" width="18" height="18" fill="url(#cabGrad)" rx="3"/>
       <rect x="-3" y="-22" width="12" height="9" fill="#9cd2e6"/>
       <path d="M-3-22 L9-13" stroke="#fff" stroke-width="1" opacity="0.6"/>
       <circle class="beacon-light" cx="3" cy="-27" r="3" fill="#ffaa00"/>
       <path d="M12 0h25v9H12z" fill="#6e4428"/>
       <path d="M18-2 29-19 36-16 30 0" fill="none" stroke="#9b4931" stroke-width="4.5"/>
       ${wheel(-6,11,9.5,4)}${wheel(21,11,6.5,2.5)}${wheel(36,11,5.5,2)}
     </g>`;
 }else if(kind==="truck"){
   g.innerHTML=`
     <polygon class="headlight" points="50,-6 115,-35 115,25"/>
     <g class="rig">
       <ellipse cx="10" cy="15" rx="45" ry="7.5" fill="rgba(0,0,0,0.2)"/>
       <rect x="-30" y="-9" width="26" height="16" fill="url(#cabGrad)" rx="3"/>
       <path d="M-4-14h18l10 9V8H-4z" fill="url(#cabGrad)"/>
       <rect x="2" y="-11" width="10" height="7" fill="#9cd2e6"/>
       <circle class="beacon-light" cx="5" cy="-16" r="3" fill="#ffaa00"/>
       <path d="M16-12h40V8H16z" fill="url(#bedGrad)"/>
       <path class="load" d="M20 2c5-8 12-7 17-1 5-5 11-4 15 2v4H20z" fill="#d8952f" opacity="0"/>
       ${wheel(-18,11,6.5,2.5)}${wheel(5,11,6.5,2.5)}${wheel(33,11,5.5,2)}${wheel(50,11,5.5,2)}
     </g>`;
 }else{ /* service van */
   g.innerHTML=`
     <polygon class="headlight" points="18,-4 60,-20 60,14"/>
     <g class="rig">
       <ellipse cx="1" cy="10" rx="17" ry="4.5" fill="rgba(0,0,0,0.2)"/>
       <rect x="-14" y="-8" width="28" height="13" rx="4.5" fill="url(#vanGrad)"/>
       <rect x="-11" y="-6" width="9" height="5.5" rx="1" fill="#9cd2e6"/>
       <rect x="1" y="-6" width="6" height="5.5" rx="1" fill="#b8d8e4"/>
       <rect x="-14" y="2" width="28" height="3" fill="#9b4931"/>
       ${wheel(-8,7,4.2,1.6)}${wheel(8,7,4.2,1.6)}
     </g>`;
 }
 vehicleLayer.appendChild(g);
 const start=P[node];
 const v={g,node,kind,pos:[start[0],start[1]],angle:0,speed:kind==="tractor"?62:kind==="truck"?88:108,loadEl:g.querySelector(".load")};
 setVehiclePos(v,start[0],start[1],0);
 return v;
}

function setVehiclePos(v,x,y,angle){
 v.pos=[x,y]; v.angle=angle;
 let n=((angle%360)+540)%360-180;
 if(Math.abs(n)>90){
   const rot=n>0?n-180:n+180;
   v.g.setAttribute("transform",`translate(${x} ${y}) rotate(${rot}) scale(-1, 1)`);
 }else{
   v.g.setAttribute("transform",`translate(${x} ${y}) rotate(${n})`);
 }
}

/* Smooth rAF Movement Engine */
const movers=new Set();
let lastT=performance.now();
function tick(now){
 const dt=Math.min(60,now-lastT); lastT=now;
 for(const v of movers){
   const m=v._move;
   if(!m){movers.delete(v);continue}
   let remain=v.speed*dt/1000;
   while(remain>0&&m.i<m.path.length){
     const a=m.path[m.i-1], b=m.path[m.i], seg=dist(a,b), left=seg-m.d;
     if(seg<.01){m.i++;m.d=0;continue}
     if(remain<left){m.d+=remain;remain=0}
     else{remain-=left;m.i++;m.d=0}
   }
   if(m.i>=m.path.length){
     const end=m.path.at(-1);
     setVehiclePos(v,end[0],end[1],v.angle);
     movers.delete(v);v._move=null;v.g.classList.remove("moving");
     m.res();continue;
   }
   const a=m.path[m.i-1], b=m.path[m.i], seg=dist(a,b), t=seg<.01?1:m.d/seg;
   const cx=a[0]+(b[0]-a[0])*t, cy=a[1]+(b[1]-a[1])*t;
   const target=Math.atan2(b[1]-a[1],b[0]-a[0])*180/Math.PI;
   const dAng=((target-v.angle+540)%360)-180;
   setVehiclePos(v,cx,cy,v.angle+dAng*Math.min(1,dt/90));
 }
 requestAnimationFrame(tick);
}
requestAnimationFrame(tick);

function followPath(v,path){
 if(reduce||!path||path.length<2){
   if(path&&path.length){const e=path.at(-1);setVehiclePos(v,e[0],e[1],v.angle)}
   return Promise.resolve();
 }
 path[0]=[v.pos[0],v.pos[1]];
 v.g.classList.add("moving");
 return new Promise(res=>{v._move={path,i:1,d:0,res};movers.add(v)});
}

async function driveTo(v,node){
 const path=shortest(v.node,node);
 await followPath(v,path);
 v.node=node;
}

/* Initialization: Spawn all forests at startup */
const FM=new Map();
forests.forEach(f=>FM.set(f.id,visuals(f)));
const readyQueue=[];
const depotStack=chipPile(658,447);

/* Felling: trees near the work point fall one by one, then quickly disappear */
async function fellForest(f,vis){
 const targets=[...vis.trees].sort((a,b)=>dist([a.x,a.y],f.work)-dist([b.x,b.y],f.work));
 const k=Math.ceil(targets.length*.65);
 vis.felled=targets.slice(0,k);
 vis.poly.classList.add("clearing");
 const total=vis.felled.length;
 for(let i=0;i<total;i++){
   const t=vis.felled[i];
   t.art.classList.add("wig");
   await sleep(reduce?0:130);
   t.art.classList.remove("wig");
   t.art.classList.add(i%2?"fall-r":"fall-l");
   t.stump.classList.add("show");
   await sleep(reduce?0:300);
   /* Sofort nach dem Schnitt: Baum fällt und liegt kurz als Stamm */
   t.pos.classList.add("gone");
   t.log.classList.add("show");
   await sleep(reduce?0:260);
   /* Direkt im Wald gehäckselt: Stamm weg, Hackschnitzelhaufen wächst */
   t.log.classList.remove("show");
   chipPileGrow(vis.chips,(i+1)/total);
 }
 await sleep(reduce?0:300);
 vis.state="ready";
 readyQueue.push(f.id);
}

/* Regrow: stumps & logs vanish, trees sprout back upright */
function regrowForest(vis){
 vis.state="regrow";
 vis.poly.classList.remove("clearing");
 vis.felled.forEach(t=>{t.stump.classList.remove("show");t.log.classList.remove("show")});
 vis.felled.forEach((t,i)=>{
   setTimeout(()=>{
     t.pos.classList.remove("gone");
     t.art.classList.remove("fall-l","fall-r","wig");
     t.art.classList.add("sprout");
     setTimeout(()=>t.art.classList.remove("sprout"),900);
   },reduce?0:i*70);
 });
 setTimeout(()=>{vis.state="grown";vis.felled=[]},reduce?0:vis.felled.length*70+950);
}

/* Tractor Routine: drives INTO the forest, fells, backs out, next forest */
async function tractorRoutine(v,ids){
 let i=0;
 while(true){
   const f=forests[ids[i%ids.length]];
   const vis=FM.get(f.id);
   if(vis.state!=="grown"){i++;await sleep(350);continue}
   vis.state="working";
   await driveTo(v,f.base);
   await driveTo(v,"G"+f.id);
   await driveTo(v,"T"+f.id);
   v.g.classList.add("working");
   await fellForest(f,vis);
   v.g.classList.remove("working");
   await driveTo(v,"G"+f.id);
   i++;
 }
}

/* Truck Routine: collects the chip pile from INSIDE the forest, delivers to depot */
async function truckRoutine(v,parkOffset){
 while(true){
   if(!readyQueue.length){await sleep(400);continue}
   const id=readyQueue.shift();
   const f=forests[id], vis=FM.get(id);
   if(vis.state!=="ready")continue;
   vis.state="collecting";
   await driveTo(v,f.base);
   await driveTo(v,"G"+id);
   await driveTo(v,"U"+id);
   v.g.classList.add("working");
   await sleep(reduce?0:650);
   /* Hackschnitzelhaufen wird aufgeladen */
   vis.chips.classList.remove("show");
   vis.chips._art.style.transform="scale(.2)";
   if(v.loadEl)v.loadEl.setAttribute("opacity","1");
   await sleep(reduce?0:650);
   v.g.classList.remove("working");
   await driveTo(v,"G"+id);
   await driveTo(v,"TD");
   /* Ausladen am Depot: Hackschnitzelhaufen erscheint kurz, dann abtransportiert/verwertet */
   chipPileGrow(depotStack,1);
   if(v.loadEl)v.loadEl.setAttribute("opacity","0");
   await sleep(reduce?0:1100);
   depotStack.classList.remove("show");
   depotStack._art.style.transform="scale(.2)";
   setTimeout(()=>regrowForest(vis),reduce?200:2600);
   /* Park offset so trucks don't stack at the depot node */
   const parkPath=[P.TD,[P.TD[0]+parkOffset[0],P.TD[1]+parkOffset[1]]];
   await followPath(v,densify(parkPath,14));
 }
}

/* Service Van: endless ambient round trip across the hub network */
async function vanRoutine(v){
 const tour=["W","WW","W","C","HQ","C","E","EE","E","C","N","NW","N","NE","N","C","S","MS","S","SW","S","SE","S","C"];
 let i=0;
 while(true){
   await driveTo(v,tour[i%tour.length]);
   i++;
   await sleep(reduce?0:450);
 }
}

function runSim(){
 const tractorN=vehicle("tractor","MW");   /* Nord-Wälder 0–5 */
 const tractorS=vehicle("tractor","MS");   /* Süd-Wälder 6–11 */
 const truck1=vehicle("truck","TD");
 const truck2=vehicle("truck","TD");
 const truck3=vehicle("truck","ME");
 const van=vehicle("van","HQ");

 tractorRoutine(tractorN,[0,1,2,3,4,5]);
 tractorRoutine(tractorS,[6,7,8,9,10,11]);
 truckRoutine(truck1,[-24,16]);
 truckRoutine(truck2,[-38,26]);
 truckRoutine(truck3,[-10,28]);
 vanRoutine(van);
}

runSim();
})();
