import React from "react";
import { ConnectionCard } from "../connection/ConnectionCard";
import { DiagnosticsCard } from "../diagnostics/DiagnosticsCard";
import { ActionTester } from "./ActionTester";
import { SystemCard } from "./SystemCard";
import { UpdateCard } from "./UpdateCard";

export const OverviewView: React.FC = () => (
  <div className="cards-stack">
    <ConnectionCard />
    <SystemCard />
    <UpdateCard />
    <DiagnosticsCard />
    <ActionTester />
  </div>
);

export default OverviewView;
