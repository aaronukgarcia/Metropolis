import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { totalJobsWholeCity, computeRoadConnectivity } from '../src/sim/data.ts';
import { sectorKeyOf, sectorIndexOf, dirtySectorsSince, totalJobsPartitioned, __resetDirtyTrackingForTest, __setPartitionedDerivationsForTest } from '../src/sim/sectorPartition.ts';
function ser(index){return [...index.keys()].sort((a,b)=>a-b).map(k=>{const a=index.get(k);return `${k}:${a.buildingCount}:${a.jobs}`;}).join('|');}
function cold(s){__setPartitionedDerivationsForTest(false);try{return sectorIndexOf(s);}finally{__setPartitionedDerivationsForTest(true);}}
function st(s){__setPartitionedDerivationsForTest(true);const inc=ser(sectorIndexOf(s));const pj=totalJobsPartitioned(s);const c=ser(cold(s));const wj=totalJobsWholeCity(s);return {stale:pj!==wj||inc!==c,pj,wj,dirty:dirtySectorsSince(s)};}
function settled(){__resetDirtyTrackingForTest();__setPartitionedDerivationsForTest(true);let s={...initialState(),unlockedAll:true,funds:5_000_000_000};
 for(let x=0;x<60;x++) s=reducer(s,{type:'place',spec:'road',x,y:10});
 for(let t=0;t<25;t++) s=reducer(s,{type:'tick'});
 for(const x of [2,22,42]) s=reducer(s,{type:'place',spec:'com_market',x,y:11});
 for(let t=0;t<10;t++) s=reducer(s,{type:'tick'});return s;}
function run(label, entry, firstAction){
  let live=reducer(entry, firstAction);
  const log=[];
  let r=st(live); log.push(`t0 ${r.pj}/${r.wj} stale=${r.stale} dirty=${r.dirty===null?'null':[...r.dirty]}`);
  let anyStale=r.stale;
  for(let t=1;t<=5;t++){live=reducer(live,{type:'tick'});r=st(live);log.push(`t${t} ${r.pj}/${r.wj} stale=${r.stale}`);anyStale||=r.stale;}
  console.log(label, log.join(' ; '));
  return anyStale;
}
test('A1a stripped conn + first transition places a NON-road building (topology flag false keeps stripped conn)', ()=>{
  const s=settled();
  const stripped={...s,roadConnectivity:{connectedRoadTiles:[]}};
  assert.equal(run('A1a', stripped, {type:'place',spec:'com_market',x:50,y:11}), false);
});
test('A1b stripped conn + first transition places a road', ()=>{
  const s=settled();
  const stripped={...s,roadConnectivity:{connectedRoadTiles:[]}};
  assert.equal(run('A1b', stripped, {type:'place',spec:'road',x:60,y:10}), false);
});
test('A1c absent conn + first transition places a non-road building', ()=>{
  const s=settled();
  const legacy={...s,roadConnectivity:undefined};
  assert.equal(run('A1c', legacy, {type:'place',spec:'com_market',x:50,y:11}), false);
});
test('A1d stripped conn + first transition bulldozes a road tile (cuts spine)', ()=>{
  const s=settled();
  const stripped={...s,roadConnectivity:{connectedRoadTiles:[]}};
  assert.equal(run('A1d', stripped, {type:'bulldoze',x:12,y:10}), false);
});
test('A1e FULL conn present but STALE (missing a segment) + non-road place', ()=>{
  const s=settled();
  // connectivity computed with spine x>=20 removed: markets at 22/42 offline in the carried graph
  const cut={...s,buildings:s.buildings.filter(b=>!(b.spec==='road'&&b.x>=15))};
  const staleConn={...s,roadConnectivity:computeRoadConnectivity(cut)};
  assert.equal(run('A1e', staleConn, {type:'place',spec:'com_market',x:50,y:11}), false);
});
test('A1f stripped conn + first transition bulldozes a finished NON-road building sharing a sector (no completion ever heals)', ()=>{
  let s=settled();
  s=reducer(s,{type:'place',spec:'com_market',x:46,y:11});
  for(let t=0;t<10;t++) s=reducer(s,{type:'tick'});
  console.log('A1f sameSector', sectorKeyOf(46,11)===sectorKeyOf(42,11));
  const stripped={...s,roadConnectivity:{connectedRoadTiles:[]}};
  assert.equal(run('A1f', stripped, {type:'bulldoze',x:46,y:11}), false);
});
test('A5 un-banded prev through the BUG-1137 branch is not strongly retained by next', {skip: typeof global.gc!=='function'?'expose-gc':false}, async ()=>{
  const s=settled();
  let prev={...s,roadConnectivity:{connectedRoadTiles:[]}, buildings:[...s.buildings]};
  const ref=new WeakRef(prev);
  let next=reducer(prev,{type:'place',spec:'com_market',x:50,y:11});
  assert.equal(dirtySectorsSince(next), null, 'r4: out of an un-banded prev commits FULL (dirtySectorsSince -> null)');
  prev=null;
  for(let i=0;i<6;i++){await new Promise(r=>setTimeout(r,10));global.gc();}
  assert.equal(ref.deref(), undefined, 'un-banded prev retained after its successor was committed');
  sectorIndexOf(next);
});
test('A6 r4 commit-FULL: ANY first transition out of an un-banded prev commits FULL (dirtySectorsSince -> null)', ()=>{
  for (const mk of [()=>({...settled(),roadConnectivity:{connectedRoadTiles:[]}}), ()=>({...settled(),roadConnectivity:undefined})]) {
    const next=reducer(mk(),{type:'place',spec:'com_market',x:50,y:11});
    assert.equal(dirtySectorsSince(next), null);
  }
});
