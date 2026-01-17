import React from 'react';

import { RevealOnScroll } from './RevealOnScroll';

interface ArchitectureRowProps {
    title: string;
    source: string;
    dest: string;
    toroAction: string;
    icon: React.ElementType;
    delay?: number;
}

export const ArchitectureRow = ({ title, source, dest, toroAction, icon: Icon, delay = 0 }: ArchitectureRowProps) => (
    <RevealOnScroll delay={delay} className="flex flex-col md:flex-row items-center justify-center gap-4 md:gap-8 opacity-90 mb-12 last:mb-0 relative">
        <div className="md:absolute left-0 md:left-4 xl:left-24 text-xs font-mono text-zinc-600 uppercase tracking-widest mb-2 md:mb-0 w-full md:w-auto text-center md:text-left">{title}</div>

        {/* Source */}
        <div className="p-5 border border-zinc-800 rounded-xl bg-zinc-900/50 w-64 text-center z-10 shadow-lg">
            <div className="font-mono text-zinc-500 text-[10px] mb-2 uppercase">Source</div>
            <div className="font-bold text-sm text-zinc-200">{source}</div>
        </div>

        {/* Animated Line 1 */}
        <div className="hidden md:block w-16 h-px bg-zinc-800 relative overflow-hidden">
            <div className="absolute inset-0 bg-gradient-to-r from-transparent via-green-500 to-transparent w-1/2 animate-shimmer"></div>
        </div>
        <div className="md:hidden w-px h-12 bg-zinc-800 relative overflow-hidden">
            <div className="absolute inset-0 bg-gradient-to-b from-transparent via-green-500 to-transparent h-1/2 animate-shimmer-vertical"></div>
        </div>

        {/* Toro */}
        <div className="p-4 border border-green-500/30 rounded-xl bg-green-500/5 w-64 relative z-10 hover:scale-105 transition-transform duration-300 group flex flex-col items-center gap-2 shadow-[0_0_20px_rgba(34,197,94,0.1)]">
            <div className="absolute -top-3 bg-[#030303] border border-green-500/30 rounded-full p-1 shadow-[0_0_15px_rgba(34,197,94,0.3)]">
                <Icon className="w-5 h-5 text-green-500" />
            </div>
            <div className="pt-2 font-mono text-sm text-green-400 font-bold">{toroAction}</div>
            <div className="text-[10px] text-zinc-500 font-mono">Processing...</div>
        </div>

        {/* Animated Line 2 */}
        <div className="hidden md:block w-16 h-px bg-zinc-800 relative overflow-hidden">
            <div className="absolute inset-0 bg-gradient-to-r from-transparent via-green-500 to-transparent w-1/2 animate-shimmer" style={{ animationDelay: '0.5s' }}></div>
        </div>
        <div className="md:hidden w-px h-12 bg-zinc-800 relative overflow-hidden">
            <div className="absolute inset-0 bg-gradient-to-b from-transparent via-green-500 to-transparent h-1/2 animate-shimmer-vertical" style={{ animationDelay: '0.5s' }}></div>
        </div>

        {/* Dest */}
        <div className="p-5 border border-zinc-800 rounded-xl bg-zinc-900/50 w-64 text-center z-10 shadow-lg">
            <div className="font-mono text-zinc-500 text-[10px] mb-2 uppercase">Destination</div>
            <div className="font-bold text-sm text-zinc-200">{dest}</div>
        </div>
    </RevealOnScroll>
);
