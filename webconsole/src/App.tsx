import { SimProvider } from './sim/store';
import { BusyProvider, BusyIndicator } from './components/Busy';
import { TopBar, StartOverButton } from './components/TopBar';
import { LeftDock } from './components/left/LeftDock';
import { DemandDock } from './components/left/DemandDock';
import { RightDock } from './components/right/RightDock';
import { BottomBar } from './components/bottom/BottomBar';
import { MapView } from './components/MapView';
import { PerfHud } from './components/PerfHud';
import { QueueDepthHud } from './components/right/QueueDepthHud';
import { ErrorBoundary } from './components/ErrorBoundary';
import { VersionUpgradeToast } from './sim/liveVersion';
import { OverlayManagerProvider } from './components/overlayManager';

// Layout (FEAT-1972079874): map stays centre; the docks are re-homed —
//   LEFT   : build palette (BottomBar) + Start Over button
//   BOTTOM : information panel (RightDock)
//   RIGHT  : demand (DemandDock) + fiscal (LeftDock)
//
// FEAT-2326609720 inc1: OverlayManagerProvider wraps the ENTIRE app (above
// SimProvider) because it must be a common ancestor of every blocking
// overlay candidate — RebuildPrompt is rendered by SimProvider itself
// (sim/store.tsx), while InsolvencyPopup/ForcedAssetSalesPanel/DeclineScreen
// are rendered deep inside MapView. One provider at the root is the only way
// the single-blocking-overlay invariant can span both subtrees.
export default function App() {
  return (
    <ErrorBoundary>
      <OverlayManagerProvider>
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
                  {/* BUG-499: this used to be a position:fixed overlay pinned to the
                      viewport's bottom-right corner, which drew directly over the
                      lower half of this same column's fiscal panel (LeftDock) no
                      matter what was on screen — the "thin green vertical line ...
                      over the top of other information" Aaron reported. It now
                      lives IN this flex column as its own reserved slot (3rd
                      child, after LeftDock) so it never shares screen space with a
                      sibling panel — see styles.css's .queue-depth-hud rule and
                      the .right-col > .panel:nth-child(2) rule that keeps
                      targeting LeftDock specifically now that it is no longer the
                      literal last child. */}
                  <QueueDepthHud />
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
      </OverlayManagerProvider>
    </ErrorBoundary>
  );
}
