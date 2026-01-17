import { useState, useEffect } from 'react';
import { Icons } from './Icons';

export const DashboardDemo = () => {
    const [rows, setRows] = useState([
        { id: 1, method: "POST", path: "/twilio/sms-inbound", status: 200, time: "2s ago", failed: false, replayed: false },
        { id: 2, method: "POST", path: "/stripe/payment_intent", status: 500, time: "12s ago", failed: true, replayed: false },
        { id: 3, method: "POST", path: "/plaid/transactions/sync", status: 200, time: "45s ago", failed: false, replayed: false },
    ]);
    const [replaying, setReplaying] = useState(false);
    const [analyzing, setAnalyzing] = useState(true);

    useEffect(() => {
        const diverseEvents = [
            { path: "/slack/events", method: "POST", fix: "Schema Mismatch: 'user_id' string vs int." },
            { path: "/zendesk/tickets", method: "POST", fix: "Validation: 'priority' field is missing." },
            { path: "/shopify/orders", method: "POST", fix: "Timeout: Downstream DB connection lost." }
        ];
        let eventIndex = 0;

        const interval = setInterval(() => {
            setReplaying(true);
            setAnalyzing(false); // Reset analysis UI during replay

            setTimeout(() => {
                const nextEvent = diverseEvents[eventIndex];
                eventIndex = (eventIndex + 1) % diverseEvents.length;

                setRows(prev => [
                    {
                        id: Date.now(),
                        method: nextEvent.method,
                        path: nextEvent.path,
                        status: 200,
                        time: "Just now",
                        failed: false,
                        replayed: true
                    },
                    prev[1],
                    prev[2]
                ]);
                setReplaying(false);
                // Start analysis for the static failed row again after "fix"
                setTimeout(() => setAnalyzing(true), 500);
            }, 1000);
        }, 5000);

        return () => clearInterval(interval);
    }, []);

    return (
        <div className="bg-[#0A0A0A] border border-toro-border rounded-xl shadow-2xl overflow-hidden font-mono text-xs md:text-sm relative transition-all duration-300 h-[300px] flex flex-col">
            <div className="flex items-center justify-between px-4 py-3 bg-zinc-900/50 border-b border-toro-border shrink-0">
                <div className="flex gap-4 text-zinc-500 font-bold text-[10px] tracking-wider uppercase min-w-[300px]">
                    <span className="w-16">Method</span>
                    <span className="flex-1">Endpoint</span>
                    <span className="w-20">Status</span>
                    <span className="w-16">Action</span>
                </div>
            </div>
            {/* Add overflow-auto to allow both horizontal and vertical scrolling within the fixed height */}
            <div className="p-2 space-y-1 overflow-auto flex-1 custom-scrollbar">
                <div className="min-w-[300px]">
                    {rows.map((row) => (
                        <div key={row.id}>
                            <div className={`flex items-center gap-4 px-3 py-2 rounded transition-all duration-500 ${row.replayed ? 'bg-green-900/10 border border-green-900/30' : 'hover:bg-zinc-900/40'}`}>
                                <span className={`w-16 font-bold ${row.method === 'POST' ? 'text-blue-400' : 'text-purple-400'}`}>{row.method}</span>
                                <span className="flex-1 text-zinc-300 truncate" title={row.path}>{row.path}</span>
                                <span className={`w-20 font-bold ${row.status === 200 ? 'text-green-500' : 'text-red-500'}`}>
                                    {row.status === 500 ? "500 ERR" : "200 OK"}
                                </span>
                                <div className="w-16 flex justify-end relative">
                                    {row.failed && (
                                        <button className={`p-1.5 rounded bg-zinc-800 text-zinc-400 hover:text-white hover:bg-zinc-700 transition-colors ${replaying ? 'text-green-400' : ''}`}>
                                            <Icons.RotateCw className={`w-3 h-3 ${replaying ? 'animate-spin' : ''}`} />
                                        </button>
                                    )}
                                    {row.failed && replaying && (
                                        <div className="absolute -bottom-4 -right-4 transition-transform duration-500 z-50">
                                            <svg className="w-6 h-6 text-white drop-shadow-lg" fill="currentColor" viewBox="0 0 24 24"><path d="M7 2l12 11.2-5.8.5 3.3 7.3-2.2.9-3.2-7.4-4.4 4z" /></svg>
                                        </div>
                                    )}
                                </div>
                            </div>
                            {/* AI Analysis Drawer */}
                            {row.failed && analyzing && !replaying && (
                                <div className="ml-12 mr-4 mt-1 mb-2 p-3 bg-zinc-900/80 border border-toro-border rounded-lg animate-[slideDown_0.3s_ease-out] relative overflow-hidden">
                                    <div className="absolute inset-0 bg-green-500/5 animate-pulse-slow"></div>
                                    <div className="flex gap-3 relative z-10">
                                        <Icons.Brain className="w-4 h-4 text-purple-400 mt-0.5" />
                                        <div>
                                            <div className="text-[10px] font-bold text-purple-400 mb-1 uppercase tracking-wider">AI Diagnostics</div>
                                            <div className="text-zinc-300">Crash Reason: Field <span className="text-white font-bold">'amount'</span> is null, but schema expects <span className="text-white font-bold">Integer</span>.</div>
                                        </div>
                                    </div>
                                </div>
                            )}
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
};
