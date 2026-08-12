import type { Meta, StoryObj } from '@storybook/react';
import { SummaryPanel } from './SummaryPanel';
import { mockSummary, mockSummaryQuiet, mockSummaryUnparsable } from '../../mocks/fixtures';

const meta: Meta<typeof SummaryPanel> = {
  title: 'Components/SummaryPanel',
  component: SummaryPanel,
  tags: ['autodocs'],
};

export default meta;
type Story = StoryObj<typeof SummaryPanel>;

export const WithTodos: Story = {
  args: { summary: mockSummary },
};

// 対応が要らないときは、無理に TODO を並べず 1 行で済ませる。
export const Quiet: Story = {
  args: { summary: mockSummaryQuiet },
};

// 出力を解釈できなかった場合は、生成物を捨てずに生の出力を見せる。
export const Unparsable: Story = {
  args: { summary: mockSummaryUnparsable },
};

// まだ一度も生成されていない状態（スケジュールだけがある）。
export const NotGeneratedYet: Story = {
  args: {
    summary: { schedule: '0 9 * * 1-5', next_at: mockSummary.next_at, current: null, history: [] },
  },
};

// 定期生成が無効なら何も描画しない。
export const Disabled: Story = {
  args: { summary: { schedule: '', next_at: null, current: null, history: [] } },
};
