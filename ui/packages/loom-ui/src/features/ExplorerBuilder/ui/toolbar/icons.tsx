import React from 'react';

interface IconProps {
  readonly size?: number;
  readonly stroke?: number;
}

const Icon = ({
  size = 24,
  stroke = 2,
  children,
}: IconProps & { readonly children: React.ReactNode }) => (
  <svg
    aria-hidden="true"
    fill="none"
    height={size}
    stroke="currentColor"
    strokeLinecap="round"
    strokeLinejoin="round"
    strokeWidth={stroke}
    viewBox="0 0 24 24"
    width={size}
  >
    {children}
  </svg>
);

export const IconCopy = (props: IconProps) => (
  <Icon {...props}>
    <rect height="13" rx="2" width="13" x="8" y="8" />
    <path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" />
  </Icon>
);

export const IconTrash = (props: IconProps) => (
  <Icon {...props}>
    <path d="M4 7h16M10 11v6M14 11v6M6 7l1 14h10l1-14M9 7V4h6v3" />
  </Icon>
);
