package chat

// What a turn spends, attributed so it can be read beside what a run spends.
//
// A conversation turn is a provider invocation and costs what it costs, and
// until now the only account of that was a number shown to whoever happened to
// be at the terminal. That is enough to notice an expensive afternoon and no use
// at all afterwards: the session ends, the numbers go with it, and nothing
// durable says the management conversation cost anything.
//
// So a turn lands in the same cost log a run's invocations do, one line each. It
// is charged to the conversation rather than to a work item, deliberately: a
// conversation that discussed five items is not attributable to any one of them,
// and putting it on whichever came up last would be a guess dressed as a join.

import (
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// spendAttribution is what this turn's invocation is charged to. The role and
// the requested model are on the request the meter already sees; this is the
// rest — which conversation, which agent, and on whose account.
func (s *Session) spendAttribution() spend.Attribution {
	return spend.Attribution{
		ProductID:      s.options.ProductID,
		Agent:          s.options.Agent,
		Phase:          runstate.SpendPhaseConversation,
		AccountAlias:   s.options.AccountAlias,
		ConfigRevision: s.options.ConfigRevision,
		Backend:        s.options.Provider,
		ConversationID: s.state.ConversationID,
	}
}
