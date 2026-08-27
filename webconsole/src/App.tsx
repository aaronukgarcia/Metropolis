import { SimProvider } from './sim/store';
import { BusyProvider, BusyIndicator } from './components/Busy';
import { TopBar, StartOverButton } from './components/TopBar';
import { LeftDock } from './components/left/LeftDock';
import { DemandDock } from './components/left/DemandDock';
import { RightDock } from './components/right/RightDock';
import { BottomBar } from './components/bottom/BottomBar';
import { MapView } from './components/MapView';
import { PerfHud } from './components/PerfHud';
import { ErrorBoundary } from './components/ErrorBoundary';
import { VersionUpgradeToast } from './sim/liveVersion';

// Layout (FEAT-1972079874): map stays centre; the docks are re-homed —
//   LEFT   : build palette (BottomBar) + Start Over button
//   BOTTOM : information panel (RightDock)
//   RIGHT  : demand (DemandDock) + fiscal (LeftDock)
export default function App() {
  return (
    <ErrorBoundary>
      <BusyProvider>
        <SimProvider>
          <ErrorBoundary>
            <div className="app">
              <TopBar />
              <div className="col-wrap left-col">
                <BottomBar />
                <StartOverButton />
              </div>
              <MapView />
              <div className="col-wrap right-col">
                <DemandDock />
                <LeftDock />
              </div>
              <div className="col-wrap bottom-col">
                <RightDock />
              </div>
              <PerfHud />
            </div>
            <BusyIndicator />
          </ErrorBoundary>
          <VersionUpgradeToast />
        </SimProvider>
      </BusyProvider>
    </ErrorBoundary>
  );
}
