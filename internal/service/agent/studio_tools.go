package agent

func (s *Service) registerStudioTools(server *mcpToolset) {
	s.registerStudioPhase1Tools(server)
	s.registerStudioPhase2Tools(server)
	s.registerStudioTimelineTools(server)
	s.registerStudioTier123Tools(server)
}
