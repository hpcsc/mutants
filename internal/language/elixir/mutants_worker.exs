defmodule Mix.Tasks.Mutants.Worker do
  use Mix.Task

  # the runner of mutants reads each line with this prefix as a reply, and each other line as output of the tests
  @reply "mutants-reply "

  @impl true
  def run(args) do
    Application.put_env(:ex_unit, :autorun, false)
    Mix.Task.run("compile", args)
    Mix.Task.run("app.start", args)
    Code.put_compiler_option(:ignore_module_conflict, true)

    paths = Mix.Project.config()[:test_paths] || ["test"]

    for path <- paths, File.exists?(Path.join(path, "test_helper.exs")) do
      Code.require_file(Path.join(path, "test_helper.exs"))
    end

    {:ok, failure} = Agent.start_link(fn -> nil end)
    ExUnit.configure(seed: 0, max_failures: 1, formatters: [Mix.Tasks.Mutants.Worker.Formatter], mutants_failure: failure)
    files = Mix.Utils.extract_files(paths, Mix.Project.config()[:test_pattern] || "*_test.exs")

    case Kernel.ParallelCompiler.require(files) do
      {:ok, modules, _} -> start(Enum.filter(modules, &function_exported?(&1, :__ex_unit__, 0)), failure)
      {:error, _, _} -> reply(%{"status" => "red", "detail" => "the test files do not compile"})
    end
  end

  defp start(tests, failure) do
    case run_tests(tests, failure) do
      %{"status" => "lived"} ->
        reply(%{"status" => "ready"})
        loop(%{tests: tests, failure: failure, sources: modules_by_source()})

      %{"detail" => detail} ->
        reply(%{"status" => "red", "detail" => detail})
    end
  end

  defp loop(state) do
    case IO.read(:stdio, :line) do
      line when is_binary(line) ->
        [original, mutated] = line |> String.trim_trailing("\n") |> String.split("\t")
        reply(judge(original, mutated, state))
        loop(state)

      _end ->
        System.halt(0)
    end
  end

  defp judge(original, mutated, state) do
    real = Map.get(state.sources, original, [])

    case compile(original, mutated) do
      {:ok, modules} ->
        verdict = run_tests(state.tests, state.failure)
        restored = restore(Enum.uniq(modules ++ real))
        Map.put(verdict, "restart", not restored or not running?())

      {:error, detail} ->
        %{"status" => "not_compiled", "detail" => detail, "restart" => real == [] or not restore(real)}
    end
  end

  # a module of a file that does not compile can load before the error, so the VM loads each module of the file again
  defp modules_by_source do
    for module <- Application.spec(Mix.Project.config()[:app], :modules) || [],
        beam = :code.which(module),
        is_list(beam),
        {:ok, {^module, [compile_info: info]}} <- [:beam_lib.chunks(beam, [:compile_info])],
        source = info[:source],
        source != nil,
        reduce: %{} do
      found -> Map.update(found, List.to_string(source), [module], &[module | &1])
    end
  end

  defp compile(original, mutated) do
    {result, diagnostics} =
      Code.with_diagnostics(fn ->
        try do
          {:ok, Enum.map(Code.compile_string(File.read!(mutated), original), &elem(&1, 0))}
        rescue
          error -> {:error, Exception.message(error)}
        end
      end)

    case result do
      {:ok, modules} -> {:ok, modules}
      {:error, message} -> {:error, Enum.map_join(diagnostics, "\n", & &1.message) <> "\n" <> message}
    end
  end

  defp run_tests(tests, failure) do
    Agent.update(failure, fn _ -> nil end)

    case ExUnit.run(tests) do
      %{failures: 0} -> %{"status" => "lived"}
      _failed -> %{"status" => "killed", "detail" => Agent.get(failure, & &1) || "a test failed"}
    end
  end

  # :code.load_binary also loads over old code, and a soft purge first keeps alive a process that runs it
  defp restore(modules) do
    Enum.all?(modules, fn module ->
      :code.soft_purge(module) and
        case :code.get_object_code(module) do
          {^module, binary, file} -> match?({:module, _}, :code.load_binary(module, file, binary)) and :code.soft_purge(module)
          :error -> :code.delete(module) and :code.soft_purge(module)
        end
    end)
  end

  defp running? do
    List.keymember?(Application.started_applications(), Mix.Project.config()[:app], 0)
  end

  defp reply(fields) do
    IO.puts(@reply <> "{" <> Enum.map_join(fields, ",", fn {key, value} -> quoted(key) <> ":" <> encode(value) end) <> "}")
  end

  defp encode(value) when is_boolean(value), do: to_string(value)
  defp encode(value), do: quoted(value)

  defp quoted(text) do
    escaped =
      for <<char::utf8 <- text>>, into: "" do
        case char do
          ?" -> "\\\""
          ?\\ -> "\\\\"
          char when char < 0x20 -> "\\u" <> String.pad_leading(Integer.to_string(char, 16), 4, "0")
          char -> <<char::utf8>>
        end
      end

    ~s("#{escaped}")
  end
end

defmodule Mix.Tasks.Mutants.Worker.Formatter do
  use GenServer

  @impl true
  def init(config), do: {:ok, Keyword.fetch!(config, :mutants_failure)}

  @impl true
  def handle_cast({:test_finished, %ExUnit.Test{state: {:failed, _}} = test}, failure) do
    first(failure, "#{test.name} (#{inspect(test.module)}), #{Path.relative_to_cwd(test.tags.file)}:#{test.tags.line}")
  end

  def handle_cast({:module_finished, %ExUnit.TestModule{state: {:failed, _}} = module}, failure) do
    first(failure, "setup_all of #{inspect(module.name)} failed")
  end

  def handle_cast(_event, failure), do: {:noreply, failure}

  defp first(failure, detail) do
    Agent.update(failure, fn
      nil -> detail
      found -> found
    end)

    {:noreply, failure}
  end
end
