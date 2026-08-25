import { SimProvider } from './sim/store';
import { BusyProvider, BusyIndicator } from './components/Busy';
import { TopBar } from './components/TopBar';
import { LeftDock } from './components/left/LeftDock';
import { DemandDock } from './components/left/DemandDock';
import { RightDock } from './components/right/RightDock';
import { BottomBar } from './components/bottom/BottomBar';
import { MapView } from './components/MapView';

export default function App() {
  return (
    <BusyProvider>
      <SimProvider>
        <div className="app">
          <TopBar />
          <div className="left-col">
            <LeftDock />
            <DemandDock />
          </div>
          <MapView />
          <div className="col-wrap right-col">
            <RightDock />
          </div>
          <div className="col-wrap bottom-col">
            <BottomBar />
          </div>
        </div>
        <BusyIndicator />
      </SimProvider>
    </BusyProvider>
  );
}
