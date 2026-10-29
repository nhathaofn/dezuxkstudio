import React from "react";
import { ServerConfigCard } from "../components/ServerConfigCard";
import { EngineActionTester } from "../components/EngineActionTester";

export const OverviewView: React.FC = () => {
  return (
    <div className="cards-stack">
      <ServerConfigCard />
      <EngineActionTester />
    </div>
  );
};

export default OverviewView;
